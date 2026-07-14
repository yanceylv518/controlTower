package mysqlstore

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"controltower/server/internal/storage"
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
	if err = tx.Commit(); err != nil {
		return storage.ChannelCommand{}, false, err
	}
	return v, true, nil
}

func (s Store) ExpireStaleCommands(before time.Time) (int, error) {
	r, e := s.db.ExecContext(context.Background(), "UPDATE channel_commands SET status='expired',updated_at=? WHERE status='pending' AND created_at < ?", time.Now().UTC(), before)
	if e != nil {
		return 0, e
	}
	n, e := r.RowsAffected()
	return int(n), e
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
	_, e := s.db.ExecContext(context.Background(), `INSERT IGNORE INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,before_summary,after_summary,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, v.ID, v.InstanceID, v.OperationType, v.TargetType, v.TargetID, v.ActorID, v.BeforeSummary, v.AfterSummary, v.Status, v.CreatedAt)
	return e
}

func (s Store) QueryOperationAudits(q storage.OperationAuditQuery) ([]storage.OperationAudit, error) {
	limit, offset := storage.NormalizeCommandPagination(q.Limit, q.Offset)
	sqlText := `SELECT id,instance_id,operation_type,target_type,target_id,actor_id,before_summary,after_summary,status,created_at FROM operation_audits`
	args := []any{}
	if q.InstanceID != "" {
		sqlText += " WHERE instance_id=?"
		args = append(args, q.InstanceID)
	}
	sqlText += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, e := s.db.QueryContext(context.Background(), sqlText, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []storage.OperationAudit
	for rows.Next() {
		var v storage.OperationAudit
		if e = rows.Scan(&v.ID, &v.InstanceID, &v.OperationType, &v.TargetType, &v.TargetID, &v.ActorID, &v.BeforeSummary, &v.AfterSummary, &v.Status, &v.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s Store) PruneBefore(kind string, cutoff time.Time) (int64, error) {
	tables := map[string][2]string{"log_events": {"log_events", "created_at"}, "log_samples": {"log_samples", "created_at"}, "metric_1m": {"metric_1m", "bucket_time"}, "metric_5m": {"metric_5m", "bucket_time"}, "server_metrics": {"server_metrics_10s", "collected_at"}, "health_checks": {"health_checks", "checked_at"}, "docker_statuses": {"docker_statuses", "collected_at"}, "nginx_timing_1m": {"nginx_timing_1m", "bucket_at"}, "nginx_slow_samples": {"nginx_slow_samples", "occurred_at"}}
	v, ok := tables[kind]
	if !ok {
		return 0, sql.ErrNoRows
	}
	r, e := s.db.ExecContext(context.Background(), "DELETE FROM "+v[0]+" WHERE "+v[1]+" < ?", cutoff)
	if e != nil {
		return 0, e
	}
	return r.RowsAffected()
}
