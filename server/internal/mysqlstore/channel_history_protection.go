package mysqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Candidates are locked by the caller until deletion commits. Batch the same
// reference checks rather than performing six dependent lookups per command.
// Read references inside that transaction; any failed check aborts the batch.
func channelHistoryDeletable(ctx context.Context, tx *sql.Tx, table string, ids []any, where string, args []any, now time.Time) ([]any, error) {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	protected := make(map[string]bool)
	collect := func(query string, values ...any) error {
		rows, err := tx.QueryContext(ctx, query, values...)
		if err != nil {
			return fmt.Errorf("reference query %.110s: %w", query, err)
		}
		defer rows.Close()
		for rows.Next() {
			var id sql.NullString
			if err = rows.Scan(&id); err != nil {
				return err
			}
			if id.Valid {
				protected[id.String] = true
			}
		}
		return rows.Err()
	}
	if table == "channel_commands" {
		// Candidate parameters are site, status, creation and completion cutoffs.
		rows, err := tx.QueryContext(ctx, `SELECT /*+ MAX_EXECUTION_TIME(1000) */ probe_command_id,last_probe_command_id,circuit_status_command_id,JSON_UNQUOTE(JSON_EXTRACT(capacity_control_json,'$.pending_command_id')) FROM tuning_continuous_states WHERE instance_id=? AND channel_id IN (SELECT channel_id FROM channel_commands WHERE id IN (`+marks+`))`, append([]any{args[0]}, ids...)...)
		if err != nil {
			return nil, fmt.Errorf("current state references: %w", err)
		}
		for rows.Next() {
			var refs [4]sql.NullString
			if err = rows.Scan(&refs[0], &refs[1], &refs[2], &refs[3]); err != nil {
				rows.Close()
				return nil, err
			}
			for _, id := range refs {
				if id.Valid {
					protected[id.String] = true
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		checks := []struct {
			query string
			tail  []any
		}{
			{`SELECT /*+ MAX_EXECUTION_TIME(1000) */ round_id FROM tuning_probe_records FORCE INDEX(idx_probe_history_guard) WHERE round_id IN (` + marks + `) AND instance_id=? AND outcome='pending' AND deadline_at>=?`, []any{args[0], now}},
			{`SELECT /*+ MAX_EXECUTION_TIME(1000) */ command_id FROM tuning_recommendations FORCE INDEX(idx_recommendation_history_guard) WHERE command_id IN (` + marks + `) AND created_at>=?`, []any{args[2]}},
			{`SELECT /*+ MAX_EXECUTION_TIME(1000) */ command_id FROM channel_control_requests FORCE INDEX(idx_request_history_guard) WHERE command_id IN (` + marks + `) AND started_at>=?`, []any{args[2]}},
			{`SELECT /*+ MAX_EXECUTION_TIME(1000) */ id FROM operation_audits FORCE INDEX(idx_audit_command_guard) WHERE id IN (` + marks + `) AND (created_at>=? OR updated_at>=?)`, []any{args[2], args[2]}},
			{channelHistoryAuditReferenceSQL(len(ids)), []any{args[2], args[2]}},
		}
		for _, check := range checks {
			if err = collect(check.query, append(append([]any{}, ids...), check.tail...)...); err != nil {
				return nil, err
			}
		}
	} else {
		// Return audit IDs, not command IDs: multiple audits can share a command.
		if err := collect(`SELECT /*+ MAX_EXECUTION_TIME(1000) */ a.id FROM operation_audits a JOIN channel_commands c ON c.id=a.id WHERE a.id IN (`+marks+`)`, ids...); err != nil {
			return nil, err
		}
		if err := collect(`SELECT /*+ MAX_EXECUTION_TIME(1000) */ a.id FROM operation_audits a JOIN channel_commands c ON c.id=a.correlation_id WHERE a.id IN (`+marks+`)`, ids...); err != nil {
			return nil, err
		}
	}
	// Recheck terminal status and both timestamps with a current locking read.
	// No original reference predicate is omitted; it was evaluated above in bulk.
	base := where
	n := 4
	if table == "operation_audits" {
		n = 3
	}
	rows, err := tx.QueryContext(ctx, `SELECT /*+ MAX_EXECUTION_TIME(1000) */ id FROM `+table+` FORCE INDEX(PRIMARY) WHERE id IN (`+marks+`) AND `+base+` FOR UPDATE`, append(append([]any{}, ids...), args[:n]...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var deletable []any
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		if !protected[id] {
			deletable = append(deletable, id)
		}
	}
	return deletable, rows.Err()
}

// A skewed correlation index (many empty values) can turn an IN predicate into
// a full covering-index scan even with FORCE INDEX. A scalar lookup with LIMIT
// keeps each candidate as a separate indexed seek and stops at the first match.
func channelHistoryAuditReferenceSQL(count int) string {
	values := "SELECT ? AS id" + strings.Repeat(" UNION ALL SELECT ?", count-1)
	return `SELECT /*+ MAX_EXECUTION_TIME(1000) */ wanted.id FROM (` + values + `) wanted WHERE
	 (SELECT a.id FROM operation_audits a FORCE INDEX(idx_audit_correlation_guard)
	 WHERE a.correlation_id=wanted.id AND (a.created_at>=? OR a.updated_at>=?) LIMIT 1) IS NOT NULL`
}
