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

func (s Store) LoadRequestMonitorRules(ctx context.Context) (storage.RequestMonitorRules, error) {
	var raw string
	var version int64
	err := s.db.QueryRowContext(ctx, "SELECT config_json,version FROM request_monitor_rules WHERE id=1").Scan(&raw, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.DefaultRequestMonitorRules(), nil
	}
	var c storage.RequestMonitorRules
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return c, err
	}
	c.Version = version
	if !c.Valid() {
		return c, errors.New("invalid stored request monitor rules")
	}
	return c, nil
}
func (s Store) SaveRequestMonitorRules(ctx context.Context, c storage.RequestMonitorRules, actor string) error {
	if !c.Valid() {
		return errors.New("invalid request monitor rules")
	}
	expected := c.Version
	c.Version++
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if expected == 0 {
		_, err = s.db.ExecContext(ctx, "INSERT INTO request_monitor_rules(id,config_json,version,updated_by,updated_at) VALUES(1,?,1,?,?)", string(raw), actor, time.Now().UTC())
		var me *mysql.MySQLError
		if errors.As(err, &me) && me.Number == 1062 {
			return storage.ErrRequestRulesConflict
		}
		return err
	}
	result, err := s.db.ExecContext(ctx, "UPDATE request_monitor_rules SET config_json=?,version=version+1,updated_by=?,updated_at=? WHERE id=1 AND version=?", string(raw), actor, time.Now().UTC(), expected)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return storage.ErrRequestRulesConflict
	}
	return err
}
