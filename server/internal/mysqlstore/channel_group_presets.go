package mysqlstore

import (
	"context"
	"controltower/server/internal/storage"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/go-sql-driver/mysql"
	"time"
)

func (s Store) LoadChannelGroupPresets(ctx context.Context, site string) (storage.ChannelGroupPresets, error) {
	result := storage.ChannelGroupPresets{Items: []storage.ChannelGroupPreset{}}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT items_json,revision FROM channel_group_presets WHERE site_id=?`, site).Scan(&raw, &result.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	err = json.Unmarshal([]byte(raw), &result.Items)
	return result, err
}

// A whole collection is updated atomically; stale editors must reload instead of overwriting newer changes.
func (s Store) SaveChannelGroupPresets(ctx context.Context, site string, value storage.ChannelGroupPresets, actor string, now time.Time) error {
	raw, err := json.Marshal(value.Items)
	if err != nil {
		return err
	}
	if value.Revision == 0 {
		_, err = s.db.ExecContext(ctx, `INSERT INTO channel_group_presets(site_id,items_json,revision,updated_by,updated_at) VALUES(?,?,1,?,?)`, site, string(raw), actor, now)
		var duplicate *mysql.MySQLError
		if errors.As(err, &duplicate) && duplicate.Number == 1062 {
			return storage.ErrGroupPresetConflict
		}
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE channel_group_presets SET items_json=?,revision=revision+1,updated_by=?,updated_at=? WHERE site_id=? AND revision=?`, string(raw), actor, now, site, value.Revision)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return storage.ErrGroupPresetConflict
	}
	return nil
}
