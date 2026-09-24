package mysqlstore

import (
	"context"
	"controltower/server/internal/tuning"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"controltower/server/internal/channelupdates"
	"controltower/server/internal/storage"
	"github.com/go-sql-driver/mysql"
)

func (s Store) CreateChannelCommand(v storage.ChannelCommand) error {
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO channel_commands
(id,instance_id,channel_id,command_type,payload_json,status,created_by,error_summary,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?)`, v.ID, v.InstanceID, v.ChannelID, v.CommandType, v.PayloadJSON, v.Status, v.CreatedBy, v.ErrorSummary, v.CreatedAt, v.UpdatedAt)
	return err
}

func (s Store) ClaimPendingCommands(instanceID string, now time.Time) ([]storage.ChannelCommand, error) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,instance_id,channel_id,command_type,payload_json,status,created_by,error_summary,created_at,updated_at
FROM channel_commands WHERE instance_id=? AND status='pending' ORDER BY created_at FOR UPDATE`, instanceID)
	if err != nil {
		return nil, err
	}
	var out []storage.ChannelCommand
	for rows.Next() {
		var v storage.ChannelCommand
		if err = rows.Scan(&v.ID, &v.InstanceID, &v.ChannelID, &v.CommandType, &v.PayloadJSON, &v.Status, &v.CreatedBy, &v.ErrorSummary, &v.CreatedAt, &v.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	valid := out[:0]
	for _, cmd := range out {
		var rec tuning.Recommendation
		err := tx.QueryRowContext(ctx, `SELECT instance_id,channel_id,rule,mode_at_creation,proposed_priority FROM tuning_recommendations WHERE command_id=? AND rule='base_priority_sync'`, cmd.ID).Scan(&rec.InstanceID, &rec.ChannelID, &rec.Rule, &rec.ModeAtCreation, &rec.ProposedPriority)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if err == nil {
			err = checkPrioritySync(tx, rec)
			if errors.Is(err, ErrPrioritySyncSuperseded) {
				if _, err = tx.ExecContext(ctx, `UPDATE channel_commands SET status='expired',error_summary='priority target or mode changed',updated_at=? WHERE id=?`, now, cmd.ID); err != nil {
					return nil, err
				}
				if _, err = tx.ExecContext(ctx, `UPDATE operation_audits SET status='expired',error_summary='priority target or mode changed',updated_at=? WHERE id=? AND status='submitted'`, now, cmd.ID); err != nil {
					return nil, err
				}
				if _, err = tx.ExecContext(ctx, `UPDATE tuning_recommendations SET status='expired',outcome_at=? WHERE command_id=?`, now, cmd.ID); err != nil {
					return nil, err
				}
				continue
			}
			if err != nil {
				return nil, err
			}
		}
		// A queued circuit transition must still belong to an active auto
		// recovery when the Agent actually claims it, not only when enqueued.
		if cmd.CommandType == "channel.update" {
			var payload struct {
				Status *int `json:"status"`
			}
			if json.Unmarshal([]byte(cmd.PayloadJSON), &payload) == nil && payload.Status != nil {
				var circuitRec tuning.Recommendation
				err = tx.QueryRowContext(ctx, `SELECT instance_id,channel_id,rule,mode_at_creation,proposed_weight FROM tuning_recommendations WHERE command_id=? AND rule IN ('circuit_disabled','circuit_recovered')`, cmd.ID).Scan(&circuitRec.InstanceID, &circuitRec.ChannelID, &circuitRec.Rule, &circuitRec.ModeAtCreation, &circuitRec.ProposedWeight)
				if err != nil && err != sql.ErrNoRows {
					return nil, err
				}
				if err == nil {
					circuitRec.ProposedChannelStatus = payload.Status
					if err := checkCircuitStatus(tx, circuitRec); err != nil {
						if !errors.Is(err, ErrCircuitStatusSuperseded) {
							return nil, err
						}
						if _, err = tx.ExecContext(ctx, `UPDATE channel_commands SET status='expired',error_summary='circuit target or mode changed',updated_at=? WHERE id=?`, now, cmd.ID); err != nil {
							return nil, err
						}
						if _, err = tx.ExecContext(ctx, `UPDATE operation_audits SET status='expired',error_summary='circuit target or mode changed',updated_at=? WHERE id=? AND status='submitted'`, now, cmd.ID); err != nil {
							return nil, err
						}
						if _, err = tx.ExecContext(ctx, `UPDATE tuning_recommendations SET status='expired',outcome_at=? WHERE command_id=?`, now, cmd.ID); err != nil {
							return nil, err
						}
						continue
					}
				}
			}
		}
		valid = append(valid, cmd)
	}
	out = valid
	if len(out) > 0 {
		ids := make([]string, len(out))
		args := make([]any, 0, len(out)+1)
		args = append(args, now)
		for i, v := range out {
			ids[i] = "?"
			args = append(args, v.ID)
			out[i].Status = "delivered"
			out[i].UpdatedAt = now
		}
		if _, err = tx.ExecContext(ctx, "UPDATE channel_commands SET status='delivered',updated_at=? WHERE id IN ("+strings.Join(ids, ",")+") AND status='pending'", args...); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s Store) CompleteChannelCommand(id, status, errorSummary string, now time.Time) (storage.ChannelCommand, bool, error) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storage.ChannelCommand{}, false, err
	}
	defer tx.Rollback()
	var v storage.ChannelCommand
	err = tx.QueryRowContext(ctx, `SELECT id,instance_id,channel_id,command_type,payload_json,status,created_by,error_summary,created_at,updated_at FROM channel_commands WHERE id=? FOR UPDATE`, id).Scan(&v.ID, &v.InstanceID, &v.ChannelID, &v.CommandType, &v.PayloadJSON, &v.Status, &v.CreatedBy, &v.ErrorSummary, &v.CreatedAt, &v.UpdatedAt)
	if err == sql.ErrNoRows {
		return storage.ChannelCommand{}, false, nil
	}
	if err != nil {
		return storage.ChannelCommand{}, false, err
	}
	if v.Status != "delivered" {
		return v, false, nil
	}
	_, err = tx.ExecContext(ctx, "UPDATE channel_commands SET status=?,error_summary=?,updated_at=? WHERE id=? AND status='delivered'", status, errorSummary, now, id)
	if err != nil {
		return storage.ChannelCommand{}, false, err
	}
	v.Status = status
	v.ErrorSummary = errorSummary
	v.UpdatedAt = now
	siteID := siteIDForInstance(tx, v.InstanceID)
	if status == "succeeded" && v.CommandType == "channel.update" {
		if err = applyCompletedChannelWrite(tx, v, now); err != nil {
			return storage.ChannelCommand{}, false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return storage.ChannelCommand{}, false, err
	}
	channelupdates.Notify(siteID)
	return v, true, nil
}

func (s Store) ExpireStaleCommands(before time.Time) (int, error) {
	total := 0
	for {
		var n int
		var done bool
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			n, done, err = s.expireStaleCommandsBatch(before)
			var databaseError *mysql.MySQLError
			if !errors.As(err, &databaseError) || databaseError.Number != 1213 {
				break
			}
		}
		if err != nil {
			return total, err
		}
		total += n
		if done {
			return total, nil
		}
	}
}

const expiredCommandBatchSize = 100

// Discover candidates without locking the status index: even an empty locking
// range scan can touch its first nonmatching row and deadlock with delivery.
// Candidates are rechecked under primary-key locks before any writes.
const expiredCommandCandidatesSQL = `SELECT id FROM channel_commands FORCE INDEX (idx_channel_commands_expiry)
WHERE status='pending' AND created_at < ? ORDER BY created_at,id LIMIT ?`

func (s Store) expireStaleCommandsBatch(before time.Time) (int, bool, error) {
	ctx := context.Background()
	// No snapshot is needed: candidates are locked and rechecked by each batch.
	// READ COMMITTED releases locks on candidates that no longer match and
	// avoids retaining gap locks if a candidate was removed concurrently.
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if e != nil {
		return 0, false, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, expiredCommandCandidatesSQL, before, expiredCommandBatchSize)
	if e != nil {
		return 0, false, e
	}
	var ids []string
	var idArgs []any
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return 0, false, e
		}
		ids = append(ids, "?")
		idArgs = append(idArgs, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, false, e
	}
	if len(ids) == 0 {
		return 0, true, tx.Commit()
	}
	// Lock in primary-key order, not creation-time order. A concurrent cleaner
	// or claimant may already have transitioned a discovered candidate; only
	// rows still eligible under these locks can have their audits expired.
	lockArgs := append(append([]any{}, idArgs...), before)
	rows, e = tx.QueryContext(ctx, "SELECT id FROM channel_commands FORCE INDEX (PRIMARY) WHERE id IN ("+strings.Join(ids, ",")+") AND status='pending' AND created_at < ? ORDER BY id FOR UPDATE", lockArgs...)
	if e != nil {
		return 0, false, e
	}
	ids, idArgs = nil, nil
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return 0, false, e
		}
		ids = append(ids, "?")
		idArgs = append(idArgs, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, false, e
	}
	if len(ids) == 0 {
		// Do not stop because another cleaner consumed this batch. There may
		// still be further candidates beyond the initial LIMIT.
		return 0, false, tx.Commit()
	}
	now := time.Now().UTC()
	args := append([]any{now}, idArgs...)
	r, e := tx.ExecContext(ctx, "UPDATE channel_commands FORCE INDEX (PRIMARY) SET status='expired',updated_at=? WHERE id IN ("+strings.Join(ids, ",")+") AND status='pending'", args...)
	if e != nil {
		return 0, false, e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE operation_audits FORCE INDEX (PRIMARY) SET status='expired',error_summary='command expired before execution',updated_at=? WHERE id IN ("+strings.Join(ids, ",")+") AND status='submitted'", args...); e != nil {
		return 0, false, e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return 0, false, e
	}
	if e = tx.Commit(); e != nil {
		return 0, false, e
	}
	return int(n), false, nil
}

func (s Store) QueryChannelCommands(q storage.ChannelCommandQuery) ([]storage.ChannelCommand, error) {
	limit, offset := storage.NormalizeCommandPagination(q.Limit, q.Offset)
	where := []string{}
	args := []any{}
	if q.InstanceID != "" {
		where = append(where, "instance_id=?")
		args = append(args, q.InstanceID)
	}
	if q.Status != "" {
		where = append(where, "status=?")
		args = append(args, q.Status)
	}
	sqlText := `SELECT id,instance_id,channel_id,command_type,payload_json,status,created_by,error_summary,created_at,updated_at FROM channel_commands`
	if len(where) > 0 {
		sqlText += " WHERE " + strings.Join(where, " AND ")
	}
	sqlText += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, e := s.db.QueryContext(context.Background(), sqlText, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []storage.ChannelCommand
	for rows.Next() {
		var v storage.ChannelCommand
		if e = rows.Scan(&v.ID, &v.InstanceID, &v.ChannelID, &v.CommandType, &v.PayloadJSON, &v.Status, &v.CreatedBy, &v.ErrorSummary, &v.CreatedAt, &v.UpdatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s Store) InsertOperationAudit(v storage.OperationAudit) error {
	v = storage.NormalizeOperationAudit(v)
	if !storage.IsManualOperationAudit(v) {
		return nil
	}
	if storage.IsExcludedOperationAudit(v.OperationType) {
		return nil
	}
	if !storage.IsSupportedOperationAudit(v.OperationType) {
		return storage.ErrUnsupportedOperationAudit
	}
	_, e := s.db.ExecContext(context.Background(), `INSERT INTO operation_audits
(id,instance_id,operation_type,target_type,target_id,actor_id,actor_type,actor_role,source_component,trigger_type,request_id,correlation_id,client_ip,auth_method,http_method,route,http_status,error_summary,before_summary,after_summary,status,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON DUPLICATE KEY UPDATE
after_summary=IF(status='submitted' AND VALUES(status)<>'submitted',VALUES(after_summary),after_summary),
error_summary=IF(status='submitted' AND VALUES(status)<>'submitted',VALUES(error_summary),error_summary),
http_status=IF(status='submitted' AND VALUES(status)<>'submitted',VALUES(http_status),http_status),
updated_at=IF(status='submitted' AND VALUES(status)<>'submitted',VALUES(updated_at),updated_at),
status=IF(status='submitted' AND VALUES(status)<>'submitted',VALUES(status),status)`,
		v.ID, v.InstanceID, v.OperationType, v.TargetType, v.TargetID, v.ActorID, v.ActorType, v.ActorRole, v.SourceComponent, v.TriggerType, v.RequestID, v.CorrelationID, v.ClientIP, v.AuthMethod, v.HTTPMethod, v.Route, v.HTTPStatus, v.ErrorSummary, v.BeforeSummary, v.AfterSummary, v.Status, v.CreatedAt, v.UpdatedAt)
	return e
}

func (s Store) UpdateOperationAuditHTTPStatus(requestID string, status int) error {
	if requestID == "" {
		return nil
	}
	_, err := s.db.ExecContext(context.Background(), `UPDATE operation_audits SET http_status=? WHERE request_id=?`, status, requestID)
	return err
}

func (s Store) QueryOperationAudits(q storage.OperationAuditQuery) (storage.OperationAuditPage, error) {
	return s.QueryOperationAuditsContext(context.Background(), q)
}

func (s Store) QueryOperationAuditsContext(ctx context.Context, q storage.OperationAuditQuery) (storage.OperationAuditPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	limit, offset := storage.NormalizeCommandPagination(q.Limit, q.Offset)
	where := []string{"a.is_manual_audit=1"}
	args := []any{}
	if q.InstanceID != "" {
		where = append(where, "a.instance_id=?")
		args = append(args, q.InstanceID)
	}
	if q.SiteID != "" {
		where = append(where, "(a.instance_id=? OR COALESCE(NULLIF(i.site_id,''),i.id)=?)")
		args = append(args, q.SiteID, q.SiteID)
	}
	if q.OperationType != "" {
		if prefix, ok := storage.OperationAuditTypeFilterPrefix(q.OperationType); ok {
			escapedPrefix := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(prefix)
			where = append(where, "a.operation_type LIKE ? ESCAPE '!'")
			args = append(args, escapedPrefix+"%")
		} else {
			where = append(where, "a.operation_type=?")
			args = append(args, q.OperationType)
		}
	}
	if q.Actor != "" && q.ActorExact {
		where = append(where, "a.actor_id=?")
		args = append(args, q.Actor)
	} else if q.Actor != "" {
		where = append(where, "a.actor_id LIKE ? ESCAPE '!'")
		args = append(args, "%"+strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(q.Actor)+"%")
	}
	if q.RequestID != "" {
		where = append(where, "a.request_id=?")
		args = append(args, q.RequestID)
	}
	if q.CorrelationID != "" {
		where = append(where, "a.correlation_id=?")
		args = append(args, q.CorrelationID)
	}
	if q.Status == "success" || q.Status == "succeeded" {
		where = append(where, "a.status IN ('success','succeeded')")
	} else if q.Status != "" {
		where = append(where, "a.status=?")
		args = append(args, q.Status)
	}
	if q.Source != "" {
		where = append(where, "a.source_component=?")
		args = append(args, q.Source)
	}
	if q.Trigger != "" {
		where = append(where, "a.trigger_type=?")
		args = append(args, q.Trigger)
	}
	if !q.From.IsZero() {
		where = append(where, "a.created_at>=?")
		args = append(args, q.From)
	}
	if !q.To.IsZero() {
		where = append(where, "a.created_at<?")
		args = append(args, q.To)
	}
	if q.Search != "" {
		where = append(where, "(a.operation_type LIKE ? ESCAPE '!' OR a.target_type LIKE ? ESCAPE '!' OR a.target_id LIKE ? ESCAPE '!' OR a.actor_id LIKE ? ESCAPE '!' OR a.error_summary LIKE ? ESCAPE '!' OR a.request_id LIKE ? ESCAPE '!' OR a.correlation_id LIKE ? ESCAPE '!')")
		pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(q.Search) + "%"
		args = append(args, pattern, pattern, pattern, pattern, pattern, pattern, pattern)
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}
	var page storage.OperationAuditPage
	page.OperationTypes = storage.OperationAuditFilterTypes()
	fromSQL := " FROM operation_audits a"
	if q.SiteID != "" {
		fromSQL += " LEFT JOIN instances i ON i.id=a.instance_id"
	}
	if q.ActorOptions {
		page.Actors = []string{}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT a.actor_id"+fromSQL+whereSQL+" AND a.actor_id<>'' ORDER BY a.actor_id LIMIT 100", args...)
		if err != nil {
			return page, err
		}
		defer rows.Close()
		for rows.Next() {
			var actor string
			if err := rows.Scan(&actor); err != nil {
				return page, err
			}
			page.Actors = append(page.Actors, actor)
		}
		return page, rows.Err()
	}
	if !q.ListOnly || q.CountOnly {
		count := func() (int64, error) {
			var total int64
			err := s.db.QueryRowContext(ctx, "SELECT COUNT(*)"+fromSQL+whereSQL, args...).Scan(&total)
			return total, err
		}
		var err error
		if q.CountOnly {
			page.Total, err = s.auditCounts.count(ctx, q, count)
		} else {
			page.Total, err = count()
		}
		if err != nil {
			return page, err
		}
	} else {
		page.Total = -1
	}
	if q.CountOnly || (!q.ListOnly && int64(offset) >= page.Total) {
		page.Items = []storage.OperationAudit{}
		return page, nil
	}
	if !q.BeforeTime.IsZero() && q.BeforeID != "" {
		whereSQL += " AND (a.created_at < ? OR (a.created_at = ? AND a.id < ?))"
		args = append(args, q.BeforeTime, q.BeforeTime, q.BeforeID)
		offset = 0
	}
	sqlText := `SELECT a.id,a.instance_id,a.operation_type,a.target_type,a.target_id,a.actor_id,a.actor_type,a.actor_role,a.source_component,a.trigger_type,a.request_id,a.correlation_id,a.client_ip,a.auth_method,a.http_method,a.route,a.http_status,a.error_summary,a.before_summary,a.after_summary,a.status,a.created_at,a.updated_at` + fromSQL + whereSQL + ` ORDER BY a.created_at DESC,a.id DESC LIMIT ? OFFSET ?`
	args = append(args, limit+1, offset)
	rows, e := s.db.QueryContext(ctx, sqlText, args...)
	if e != nil {
		return page, e
	}
	defer rows.Close()
	for rows.Next() {
		var v storage.OperationAudit
		if e = rows.Scan(&v.ID, &v.InstanceID, &v.OperationType, &v.TargetType, &v.TargetID, &v.ActorID, &v.ActorType, &v.ActorRole, &v.SourceComponent, &v.TriggerType, &v.RequestID, &v.CorrelationID, &v.ClientIP, &v.AuthMethod, &v.HTTPMethod, &v.Route, &v.HTTPStatus, &v.ErrorSummary, &v.BeforeSummary, &v.AfterSummary, &v.Status, &v.CreatedAt, &v.UpdatedAt); e != nil {
			return page, e
		}
		page.Items = append(page.Items, storage.NormalizeOperationAudit(v))
	}
	if page.Items == nil {
		page.Items = []storage.OperationAudit{}
	}
	if len(page.Items) > limit {
		page.HasMore = true
		page.Items = page.Items[:limit]
	}
	return page, rows.Err()
}

// pruneBatchSize bounds each retention DELETE so every statement finishes
// well inside the 30s driver read timeout: an unbatched DELETE over a large
// backlog would time out, roll back and retry with an even larger backlog
// next hour — permanently stuck. Batches always terminate.
const pruneBatchSize = 20000

type pruneExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s Store) PruneBefore(kind string, cutoff time.Time) (int64, error) {
	if kind == "alerts" {
		// Age out by last-seen time regardless of status: anything still
		// firing gets its last_seen_at refreshed every evaluation cycle, so
		// only genuinely stale history is removed.
		return pruneInBatches(s.db, `DELETE FROM alerts WHERE last_seen_at < ? LIMIT ?`, cutoff)
	}
	tables := map[string][2]string{"log_events": {"log_events", "created_at"}, "log_samples": {"log_samples", "created_at"}, "metric_1m": {"metric_1m", "bucket_time"}, "metric_5m": {"metric_5m", "bucket_time"}, "server_metrics": {"server_metrics_10s", "collected_at"}, "health_checks": {"health_checks", "checked_at"}, "docker_statuses": {"docker_statuses", "collected_at"}, "nginx_timing_1m": {"nginx_timing_1m", "bucket_at"}, "nginx_slow_samples": {"nginx_slow_samples", "occurred_at"}, "alert_events": {"alert_events", "created_at"}, "notification_deliveries": {"notification_deliveries", "attempted_at"}}
	v, ok := tables[kind]
	if !ok {
		return 0, sql.ErrNoRows
	}
	return pruneInBatches(s.db, "DELETE FROM "+v[0]+" WHERE "+v[1]+" < ? LIMIT ?", cutoff)
}

func pruneInBatches(executor pruneExecutor, query string, cutoff time.Time) (int64, error) {
	var total int64
	for {
		r, e := executor.ExecContext(context.Background(), query, cutoff, pruneBatchSize)
		if e != nil {
			return total, e
		}
		affected, e := r.RowsAffected()
		if e != nil {
			return total, e
		}
		total += affected
		if affected < pruneBatchSize {
			return total, nil
		}
	}
}
