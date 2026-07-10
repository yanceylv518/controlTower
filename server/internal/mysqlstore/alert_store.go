package mysqlstore

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"controltower/server/internal/storage"
)

func (s Store) UpsertCurrentAlerts(alerts []storage.Alert, now time.Time) error {
	for _, alert := range alerts {
		if alert.FirstSeenAt.IsZero() {
			alert.FirstSeenAt = now
		}
		if alert.LastSeenAt.IsZero() {
			alert.LastSeenAt = now
		}
		_, err := s.db.ExecContext(context.Background(), `
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
	}
	return nil
}

func (s Store) ResolveMissingAlerts(currentIDs []string, now time.Time) error {
	args := []any{now}
	sqlText := "UPDATE alerts SET status = 'resolved', resolved_at = ? WHERE status <> 'resolved'"
	if len(currentIDs) > 0 {
		placeholders := make([]string, 0, len(currentIDs))
		for _, id := range currentIDs {
			placeholders = append(placeholders, "?")
			args = append(args, id)
		}
		sqlText += " AND id NOT IN (" + strings.Join(placeholders, ",") + ")"
	}
	_, err := s.db.ExecContext(context.Background(), sqlText, args...)
	return err
}

func (s Store) ExpireSilencedAlerts(now time.Time) error {
	_, err := s.db.ExecContext(context.Background(), `
UPDATE alerts
SET status = 'firing', silence_until = NULL
WHERE status = 'silenced' AND silence_until IS NOT NULL AND silence_until <= ?`, now)
	return err
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
