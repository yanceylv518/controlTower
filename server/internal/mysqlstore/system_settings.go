package mysqlstore

import (
	"context"
	"time"

	"controltower/server/internal/storage"
)

func (s Store) ListSystemSettings() ([]storage.SystemSetting, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT setting_key,setting_value,updated_at,updated_by FROM system_settings ORDER BY setting_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.SystemSetting
	for rows.Next() {
		var item storage.SystemSetting
		if err = rows.Scan(&item.Key, &item.Value, &item.UpdatedAt, &item.UpdatedBy); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s Store) ReplaceSystemSettings(values map[string]string, actor string, now time.Time) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM system_settings`); err != nil {
		return err
	}
	for key, value := range values {
		if _, err = tx.Exec(`INSERT INTO system_settings(setting_key,setting_value,updated_at,updated_by) VALUES(?,?,?,?)`, key, value, now, actor); err != nil {
			return err
		}
	}
	return tx.Commit()
}
