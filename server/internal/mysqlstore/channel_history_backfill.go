package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Backfill only immutable, command-linked evidence. Today's instance membership
// or direct-control setting cannot prove how a historical command was executed.
// Each row is examined once; unresolved/conflicting evidence stays quarantined
// (site_id empty), excluded from notification import and automatic deletion.
func (s Store) backfillChannelHistory(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	for _, table := range []string{"channel_commands", "operation_audits"} {
		if err := s.backfillChannelHistoryBatch(ctx, table); err != nil {
			return err
		}
	}
	return nil
}

func (s Store) backfillChannelHistoryBatch(ctx context.Context, table string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	filter := "ownership_evidence=''"
	if table == "operation_audits" {
		filter += " AND target_type='channel'"
	}
	rows, err := tx.QueryContext(ctx, "SELECT id FROM "+table+" WHERE "+filter+" ORDER BY id LIMIT 100 FOR UPDATE SKIP LOCKED")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
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
	for _, id := range ids {
		if table == "operation_audits" {
			var pending bool
			err = tx.QueryRowContext(ctx, `SELECT COALESCE(c.ownership_evidence='',false) OR COALESCE(linked.ownership_evidence='',false) FROM operation_audits a LEFT JOIN channel_commands c ON c.id=a.id LEFT JOIN channel_commands linked ON a.correlation_id<>'' AND linked.id=a.correlation_id WHERE a.id=?`, id).Scan(&pending)
			if err != nil {
				return err
			}
			if pending {
				continue
			}
			// Linked command ownership is authoritative; conflicting links stay unknown.
			var site sql.NullString
			err = tx.QueryRowContext(ctx, `SELECT CASE WHEN c.site_id<>'' AND linked.site_id<>'' AND c.site_id<>linked.site_id THEN '' ELSE COALESCE(NULLIF(c.site_id,''),linked.site_id,'') END FROM operation_audits a LEFT JOIN channel_commands c ON c.id=a.id LEFT JOIN channel_commands linked ON a.correlation_id<>'' AND linked.id=a.correlation_id WHERE a.id=?`, id).Scan(&site)
			if err != nil {
				return err
			}
			evidence := "unresolved"
			if site.Valid && site.String != "" {
				evidence = "linked_command"
			}
			// Unlinked legacy channel audit rows require manual evidence review.
			if _, err = tx.ExecContext(ctx, "UPDATE operation_audits SET site_id=?,ownership_evidence=? WHERE id=?", site.String, evidence, id); err != nil {
				return err
			}
			continue
		}
		var original string
		if err = tx.QueryRowContext(ctx, "SELECT instance_id FROM channel_commands WHERE id=?", id).Scan(&original); err != nil {
			return err
		}
		evidenceRows, err := tx.QueryContext(ctx, `SELECT instance_id,'recommendation' FROM tuning_recommendations WHERE command_id=?
UNION SELECT instance_id,'direct_request' FROM channel_control_requests WHERE command_id=?
UNION SELECT j.instance_id,'direct_request' FROM tuning_recommendations r JOIN channel_control_requests j ON j.recommendation_id=r.id WHERE r.command_id=?`, id, id, id)
		if err != nil {
			return err
		}
		sites := map[string]bool{}
		direct := false
		for evidenceRows.Next() {
			var site, kind string
			if err = evidenceRows.Scan(&site, &kind); err != nil {
				evidenceRows.Close()
				return err
			}
			if site != "" {
				sites[site] = true
			}
			direct = direct || kind == "direct_request"
		}
		err = evidenceRows.Err()
		evidenceRows.Close()
		if err != nil {
			return err
		}
		// The direct marker is positive evidence; absence is never Agent evidence.
		var summary, auditOwner sql.NullString
		err = tx.QueryRowContext(ctx, "SELECT after_summary,instance_id FROM operation_audits WHERE id=?", id).Scan(&summary, &auditOwner)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		var audit struct {
			Direct bool `json:"direct"`
			Result struct {
				Direct    bool   `json:"direct"`
				AppliedAt string `json:"applied_at"`
			} `json:"result"`
		}
		_ = json.Unmarshal([]byte(summary.String), &audit)
		direct = direct || audit.Direct || audit.Result.Direct
		if (audit.Direct || audit.Result.Direct) && auditOwner.String != "" {
			sites[auditOwner.String] = true
		}
		applied, parseErr := time.Parse(time.RFC3339Nano, audit.Result.AppliedAt)
		agent := parseErr == nil && !applied.IsZero() && !audit.Result.Direct && !audit.Direct
		site, proof, kind, executor := "", "unresolved", "unknown", ""
		if len(sites) == 1 {
			for v := range sites {
				site = v
			}
			proof = "linked_evidence"
		}
		if len(sites) > 1 {
			proof = "conflict"
		}
		if direct && agent {
			site = ""
			proof = "conflict"
		} else if direct {
			kind = "ct_direct"
		} else if agent && original != "" {
			kind = "agent"
			executor = original
		}
		if _, err = tx.ExecContext(ctx, `UPDATE channel_commands SET site_id=?,execution_kind=?,instance_id=?,legacy_instance_id=?,ownership_evidence=? WHERE id=?`, site, kind, executor, original, proof, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
