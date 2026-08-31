package mysqlstore

import (
	"context"
	"time"

	"controltower/server/internal/storage"
)

// QueryUserQuotaUsage scans only the indexed user rows in metric_5m. It does
// not touch the source new-api logs table.
func (s Store) QueryUserQuotaUsage(ctx context.Context, since time.Time) ([]storage.UserQuotaUsage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id,dimension_key,SUM(request_count),SUM(quota),MIN(bucket_time)
FROM metric_5m
WHERE dimension_type='instance_user' AND bucket_time>=?
GROUP BY instance_id,dimension_key`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []storage.UserQuotaUsage{}
	for rows.Next() {
		var item storage.UserQuotaUsage
		if err := rows.Scan(&item.InstanceID, &item.DimensionKey, &item.RequestCount, &item.Quota, &item.FirstBucket); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) ListBalanceAlertUserSettings(ctx context.Context, instanceID string) (map[int64]storage.BalanceAlertUserSetting, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id,user_id,enabled,updated_at,updated_by FROM balance_alert_user_settings WHERE instance_id=?`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]storage.BalanceAlertUserSetting{}
	for rows.Next() {
		var v storage.BalanceAlertUserSetting
		if err := rows.Scan(&v.InstanceID, &v.UserID, &v.Enabled, &v.UpdatedAt, &v.UpdatedBy); err != nil {
			return nil, err
		}
		out[v.UserID] = v
	}
	return out, rows.Err()
}

func (s Store) PutBalanceAlertUserSetting(ctx context.Context, v storage.BalanceAlertUserSetting) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO balance_alert_user_settings(instance_id,user_id,enabled,updated_at,updated_by) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE enabled=VALUES(enabled),updated_at=VALUES(updated_at),updated_by=VALUES(updated_by)`, v.InstanceID, v.UserID, v.Enabled, v.UpdatedAt, v.UpdatedBy)
	return err
}
