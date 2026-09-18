package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s Store) MenuVisibility() (string, error) {
	var value string
	err := s.db.QueryRowContext(context.Background(), `SELECT items_json FROM menu_visibility WHERE id=1`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "{}", nil
	}
	return value, err
}

func (s Store) SaveMenuVisibility(value, actor string, now time.Time) error {
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO menu_visibility(id,items_json,updated_at,updated_by) VALUES(1,?,?,?) ON DUPLICATE KEY UPDATE items_json=VALUES(items_json),updated_at=VALUES(updated_at),updated_by=VALUES(updated_by)`, value, now, actor)
	return err
}
