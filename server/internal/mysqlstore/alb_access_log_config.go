package mysqlstore

import (
	"context"
	"controltower/server/internal/alblog"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/go-sql-driver/mysql"
	"time"
)

func (s Store) LoadALBLogConfig(ctx context.Context) (alblog.Config, error) {
	var c alblog.Config
	var raw, cipher string
	var version int64
	err := s.db.QueryRowContext(ctx, "SELECT config_json,secret_cipher,version FROM alb_access_log_config WHERE id=1").Scan(&raw, &cipher, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return c, alblog.ErrMissing
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return c, err
	}
	c.SecretCipher, c.Version, c.SecretSet = cipher, version, cipher != ""
	return c, nil
}
func (s Store) SaveALBLogConfig(ctx context.Context, c alblog.Config, actor string) error {
	expected := c.Version
	c.Version++
	c.SecretSet = c.SecretCipher != ""
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if expected == 0 {
		_, err = s.db.ExecContext(ctx, "INSERT INTO alb_access_log_config(id,config_json,secret_cipher,version,updated_by,updated_at) VALUES(1,?,?,1,?,?)", string(raw), c.SecretCipher, actor, time.Now().UTC())
		var me *mysql.MySQLError
		if errors.As(err, &me) && me.Number == 1062 {
			return alblog.ErrConflict
		}
		return err
	}
	result, err := s.db.ExecContext(ctx, "UPDATE alb_access_log_config SET config_json=?,secret_cipher=?,version=version+1,updated_by=?,updated_at=? WHERE id=1 AND version=?", string(raw), c.SecretCipher, actor, time.Now().UTC(), expected)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return alblog.ErrConflict
	}
	return err
}
