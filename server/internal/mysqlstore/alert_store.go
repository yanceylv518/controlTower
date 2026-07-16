package mysqlstore

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"controltower/server/internal/storage"
)

func (s Store) UpsertCurrentAlerts(alerts []storage.Alert, now time.Time) error {
	if len(alerts) == 0 {
		return nil
	}
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids := make([]string, len(alerts))
	args := make([]any, len(alerts))
	for i, a := range alerts {
		ids[i] = "?"
		args[i] = a.ID
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,status FROM alerts WHERE id IN ("+strings.Join(ids, ",")+")", args...)
	if err != nil {
		return err
	}
	states := map[string]string{}
	for rows.Next() {
		var id, status string
		if err = rows.Scan(&id, &status); err != nil {
			rows.Close()
			return err
		}
		states[id] = status
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, alert := range alerts {
		if alert.FirstSeenAt.IsZero() {
			alert.FirstSeenAt = now
		}
		if alert.LastSeenAt.IsZero() {
			alert.LastSeenAt = now
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO alerts (
  id, instance_id, rule_key, severity, status, title, summary, first_seen_at, last_seen_at, resolved_at, silence_until
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)
ON DUPLICATE KEY UPDATE
  instance_id = VALUES(instance_id),
  rule_key = VALUES(rule_key),
  severity = VALUES(severity),
  title = VALUES(title),
  summary = VALUES(summary),
  last_seen_at = VALUES(last_seen_at),
  resolved_at = NULL,
  status = CASE WHEN status = 'resolved' THEN 'firing' ELSE status END`,
			alert.ID,
			alert.InstanceID,
			alert.RuleKey,
			alert.Severity,
			alertStatusOrFiring(alert.Status),
			alert.Title,
			alert.Summary,
			alert.FirstSeenAt,
			alert.LastSeenAt,
			nullTime(alert.SilenceUntil),
		)
		if err != nil {
			return err
		}
		event := ""
		if old, ok := states[alert.ID]; !ok {
			event = "firing"
		} else if old == "resolved" {
			event = "refired"
		}
		if event != "" {
			if _, err = tx.ExecContext(ctx, "INSERT INTO alert_events(alert_id,event_type,actor,note,created_at) VALUES(?,?,'system','',?)", alert.ID, event, now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s Store) ResolveMissingAlerts(currentIDs []string, now time.Time) error {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	selectArgs := []any{}
	selectSQL := "SELECT id FROM alerts WHERE status <> 'resolved'"
	if len(currentIDs) > 0 {
		placeholders := make([]string, 0, len(currentIDs))
		for _, id := range currentIDs {
			placeholders = append(placeholders, "?")
			selectArgs = append(selectArgs, id)
		}
		selectSQL += " AND id NOT IN (" + strings.Join(placeholders, ",") + ")"
	}
	rows, err := tx.QueryContext(ctx, selectSQL, selectArgs...)
	if err != nil {
		return err
	}
	var affected []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		affected = append(affected, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	args := []any{now}
	sqlText := "UPDATE alerts SET status = 'resolved', resolved_at = ? WHERE status <> 'resolved'"
	if len(currentIDs) > 0 {
		p := make([]string, len(currentIDs))
		for i, id := range currentIDs {
			p[i] = "?"
			args = append(args, id)
		}
		sqlText += " AND id NOT IN (" + strings.Join(p, ",") + ")"
	}
	if _, err = tx.ExecContext(ctx, sqlText, args...); err != nil {
		return err
	}
	for _, id := range affected {
		if _, err = tx.ExecContext(ctx, "INSERT INTO alert_events(alert_id,event_type,actor,note,created_at) VALUES(?,'resolved','system','',?)", id, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s Store) ExpireSilencedAlerts(now time.Time) error {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id FROM alerts WHERE status='silenced' AND silence_until IS NOT NULL AND silence_until<=?", now)
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
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	_, err = tx.ExecContext(ctx, `
UPDATE alerts
SET status = 'firing', silence_until = NULL
WHERE status = 'silenced' AND silence_until IS NOT NULL AND silence_until <= ?`, now)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, "INSERT INTO alert_events(alert_id,event_type,actor,note,created_at) VALUES(?,'silence_expired','system','',?)", id, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s Store) QueryAlerts(query storage.AlertQuery) ([]storage.Alert, error) {
	sqlText, args := buildAlertQuery(query)
	rows, err := s.db.QueryContext(context.Background(), sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []storage.Alert
	for rows.Next() {
		var alert storage.Alert
		var resolvedAt sql.NullTime
		var silenceUntil sql.NullTime
		if err := rows.Scan(
			&alert.ID,
			&alert.InstanceID,
			&alert.RuleKey,
			&alert.Severity,
			&alert.Status,
			&alert.Title,
			&alert.Summary,
			&alert.FirstSeenAt,
			&alert.LastSeenAt,
			&resolvedAt,
			&silenceUntil,
		); err != nil {
			return nil, err
		}
		if resolvedAt.Valid {
			alert.ResolvedAt = &resolvedAt.Time
		}
		if silenceUntil.Valid {
			alert.SilenceUntil = &silenceUntil.Time
		}
		alerts = append(alerts, alert)
	}
	return alerts, rows.Err()
}

func (s Store) UpdateAlertAction(id string, status string, silenceUntil *time.Time, now time.Time) error {
	var resolvedAt *time.Time
	if status == "resolved" {
		resolvedAt = &now
	}
	_, err := s.db.ExecContext(context.Background(), `
UPDATE alerts
SET status = ?, silence_until = ?, resolved_at = ?
WHERE id = ?`, status, nullTime(silenceUntil), nullTime(resolvedAt), id)
	return err
}

func buildAlertQuery(query storage.AlertQuery) (string, []any) {
	limit, offset := storage.NormalizeAlertPagination(query.Limit, query.Offset)
	where := ""
	args := []any{}
	if query.InstanceID != "" {
		where, args = appendWhere(where, args, "instance_id = ?", query.InstanceID)
	}
	if query.Status != "" {
		where, args = appendWhere(where, args, "status = ?", query.Status)
	}
	if query.Severity != "" {
		where, args = appendWhere(where, args, "severity = ?", query.Severity)
	}
	if query.ActiveOnly {
		where, args = appendWhere(where, args, "status <> ?", "resolved")
	}
	args = append(args, limit, offset)
	return `SELECT id, instance_id, rule_key, severity, status, title, summary, first_seen_at, last_seen_at, resolved_at, silence_until
FROM alerts` + where + `
ORDER BY FIELD(severity, 'critical', 'warning', 'info'), last_seen_at DESC
LIMIT ? OFFSET ?`, args
}

func alertStatusOrFiring(status string) string {
	if status == "" {
		return "firing"
	}
	return status
}

// DeleteAlertsByStatus removes non-firing alerts in the given statuses along
// with their timeline events and notification deliveries.
func (s Store) DeleteAlertsByStatus(statuses []string) (int64, error) {
	if len(statuses) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(statuses)), ",")
	args := make([]any, 0, len(statuses))
	for _, status := range statuses {
		args = append(args, status)
	}
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `DELETE ae FROM alert_events ae JOIN alerts a ON ae.alert_id = a.id WHERE a.status IN (`+placeholders+`)`, args...); err != nil {
		return 0, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE nd FROM notification_deliveries nd JOIN alerts a ON nd.alert_id = a.id WHERE a.status IN (`+placeholders+`)`, args...); err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM alerts WHERE status IN (`+placeholders+`)`, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
