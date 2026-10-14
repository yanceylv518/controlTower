package mysqlstore

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Resolve the small ownership set first, so history queries use index ranges
// instead of an OR with a dependent instance-membership subquery.
func (s Store) channelHistoryOwners(ctx context.Context, site string) ([]any, error) {
	owners := []any{site}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM instances WHERE site_id=? AND id<>? ORDER BY id`, site, site)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		owners = append(owners, id)
	}
	return owners, rows.Err()
}

// Each invocation does bounded work. Terminal records must be old both by
// creation and completion time; late results receive a full retention window.
// Unknown command states and non-channel audits are deliberately preserved.
func (s Store) pruneChannelHistory(ctx context.Context, now time.Time) error {
	rows, err := s.db.QueryContext(ctx, `SELECT h.instance_id,COALESCE(NULLIF(i.site_id,''),h.instance_id) FROM (
SELECT DISTINCT instance_id FROM channel_commands
UNION SELECT DISTINCT instance_id FROM operation_audits WHERE target_type='channel'
) h LEFT JOIN instances i ON i.id=h.instance_id ORDER BY h.instance_id`)
	if err != nil {
		return err
	}
	type owner struct{ id, site string }
	var owners []owner
	for rows.Next() {
		var o owner
		if err := rows.Scan(&o.id, &o.site); err != nil {
			rows.Close()
			return err
		}
		owners = append(owners, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, o := range owners {
		days, err := s.channelHistoryDays(ctx, o.site)
		if err != nil {
			return err
		}
		before := now.Add(-time.Duration(days) * 24 * time.Hour)
		// One status per range preserves index order and bounded work.
		for _, status := range []string{"succeeded", "failed", "expired"} {
			err = s.pruneChannelHistoryBatch(ctx, "channel_commands", "idx_command_history_retention", `instance_id=?
AND status=? AND created_at<? AND updated_at<?
AND NOT EXISTS (SELECT 1 FROM tuning_continuous_states s WHERE s.instance_id=? AND s.channel_id=channel_commands.channel_id
AND (s.probe_command_id=channel_commands.id OR s.last_probe_command_id=channel_commands.id OR s.circuit_status_command_id=channel_commands.id
OR JSON_UNQUOTE(JSON_EXTRACT(s.capacity_control_json,'$.pending_command_id'))=channel_commands.id))
AND NOT EXISTS (SELECT 1 FROM tuning_probe_records p FORCE INDEX (idx_probe_round) WHERE p.instance_id=? AND p.round_id=channel_commands.id AND p.outcome='pending' AND p.deadline_at>=?)
AND NOT EXISTS (SELECT 1 FROM tuning_recommendations r WHERE r.command_id=channel_commands.id AND r.created_at>=?)
AND NOT EXISTS (SELECT 1 FROM channel_control_requests j FORCE INDEX (idx_control_command) WHERE j.command_id=channel_commands.id AND j.started_at>=?)
AND NOT EXISTS (SELECT 1 FROM operation_audits a WHERE a.id=channel_commands.id AND (a.created_at>=? OR a.updated_at>=?))
AND NOT EXISTS (SELECT 1 FROM operation_audits a FORCE INDEX (idx_operation_audits_correlation) WHERE a.correlation_id=channel_commands.id AND (a.created_at>=? OR a.updated_at>=?))
`, o.id, status, before, before, o.site, o.site, now, before, before, before, before, before, before)
			if err != nil {
				return fmt.Errorf("channel command retention: %w", err)
			}
		}
		// A linked audit becomes eligible only after its safely expired command
		// has been removed. Commands with current references keep their audits.
		err = s.pruneChannelHistoryBatch(ctx, "operation_audits", "idx_audit_channel_retention", `instance_id=? AND target_type='channel'
AND created_at<? AND updated_at<? AND status IN ('succeeded','success','failed','expired','canceled','cancelled','rejected')
AND NOT EXISTS (SELECT 1 FROM channel_commands c WHERE c.id=operation_audits.id)
AND NOT EXISTS (SELECT 1 FROM channel_commands c WHERE c.id=operation_audits.correlation_id)
`, o.id, before, before)
		if err != nil {
			return fmt.Errorf("channel audit retention: %w", err)
		}

	}
	return nil
}

// Lock an index-ordered bounded candidate set, then delete only those primary
// keys. A direct DELETE with correlated guards can choose an unordered scan and
// evaluate every old row before applying LIMIT. Never concatenate caller input.
func (s Store) pruneChannelHistoryBatch(ctx context.Context, table, index, where string, args ...any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id FROM "+table+" FORCE INDEX ("+index+") WHERE "+where+" ORDER BY created_at,id LIMIT 1000 FOR UPDATE", args...)
	if err != nil {
		return err
	}
	var ids []any
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		_, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+")", ids...)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
