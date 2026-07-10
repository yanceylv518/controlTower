package mysqlstore

import (
	"context"
	"database/sql"
	"time"

	"controltower/server/internal/storage"
)

func (s Store) UpsertNotificationChannel(channel storage.NotificationChannel) error {
	_, err := s.db.ExecContext(context.Background(), `
INSERT INTO notification_channels (
  id, channel_type, name, webhook_url, secret_value, enabled, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  channel_type = VALUES(channel_type),
  name = VALUES(name),
  webhook_url = VALUES(webhook_url),
  secret_value = VALUES(secret_value),
  enabled = VALUES(enabled),
  updated_at = VALUES(updated_at)`,
		channel.ID,
		channel.ChannelType,
		channel.Name,
		channel.WebhookURL,
		channel.SecretValue,
		channel.Enabled,
		channel.CreatedAt,
		channel.UpdatedAt,
	)
	return err
}

func (s Store) QueryNotificationChannels(enabledOnly bool) ([]storage.NotificationChannel, error) {
	sqlText := `SELECT id, channel_type, name, webhook_url, secret_value, enabled, created_at, updated_at
FROM notification_channels`
	args := []any{}
	if enabledOnly {
		sqlText += " WHERE enabled = ?"
		args = append(args, true)
	}
	sqlText += " ORDER BY updated_at DESC"
	rows, err := s.db.QueryContext(context.Background(), sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var channels []storage.NotificationChannel
	for rows.Next() {
		var channel storage.NotificationChannel
		if err := rows.Scan(&channel.ID, &channel.ChannelType, &channel.Name, &channel.WebhookURL, &channel.SecretValue, &channel.Enabled, &channel.CreatedAt, &channel.UpdatedAt); err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}
	return channels, rows.Err()
}

func (s Store) InsertNotificationDelivery(delivery storage.NotificationDelivery) error {
	_, err := s.db.ExecContext(context.Background(), `
INSERT INTO notification_deliveries (
  id, alert_id, channel_id, status, attempted_at, next_attempt_at, attempts, status_code, error_summary
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  status = VALUES(status),
  attempted_at = VALUES(attempted_at),
  next_attempt_at = VALUES(next_attempt_at),
  attempts = notification_deliveries.attempts + 1,
  status_code = VALUES(status_code),
  error_summary = VALUES(error_summary)`,
		delivery.ID,
		delivery.AlertID,
		delivery.ChannelID,
		delivery.Status,
		delivery.AttemptedAt,
		delivery.NextAttemptAt,
		delivery.Attempts,
		delivery.StatusCode,
		delivery.ErrorSummary,
	)
	return err
}

func (s Store) NotificationDeliveryDue(alertID string, channelID string, now time.Time) (bool, error) {
	var status string
	var nextAttemptAt time.Time
	err := s.db.QueryRowContext(context.Background(), `
SELECT status, next_attempt_at FROM notification_deliveries WHERE alert_id = ? AND channel_id = ? LIMIT 1`, alertID, channelID).Scan(&status, &nextAttemptAt)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if status == "sent" {
		return false, nil
	}
	return !nextAttemptAt.After(now), nil
}

func (s Store) QueryNotificationDeliveries(query storage.NotificationDeliveryQuery) ([]storage.NotificationDelivery, error) {
	sqlText, args := buildNotificationDeliveryQuery(query)
	rows, err := s.db.QueryContext(context.Background(), sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var deliveries []storage.NotificationDelivery
	for rows.Next() {
		var delivery storage.NotificationDelivery
		if err := rows.Scan(&delivery.ID, &delivery.AlertID, &delivery.ChannelID, &delivery.Status, &delivery.AttemptedAt, &delivery.NextAttemptAt, &delivery.Attempts, &delivery.StatusCode, &delivery.ErrorSummary); err != nil {
			return nil, err
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

func buildNotificationDeliveryQuery(query storage.NotificationDeliveryQuery) (string, []any) {
	limit, offset := storage.NormalizeNotificationPagination(query.Limit, query.Offset)
	where := ""
	args := []any{}
	if query.AlertID != "" {
		where, args = appendWhere(where, args, "alert_id = ?", query.AlertID)
	}
	if query.ChannelID != "" {
		where, args = appendWhere(where, args, "channel_id = ?", query.ChannelID)
	}
	if query.Status != "" {
		where, args = appendWhere(where, args, "status = ?", query.Status)
	}
	args = append(args, limit, offset)
	return `SELECT id, alert_id, channel_id, status, attempted_at, next_attempt_at, attempts, status_code, error_summary
FROM notification_deliveries` + where + `
ORDER BY attempted_at DESC
LIMIT ? OFFSET ?`, args
}

func newNotificationTimestamp() time.Time {
	return time.Now().UTC()
}
