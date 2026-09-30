package mysqlstore

import (
	"context"
	"database/sql"
	"fmt"
)

func (s Store) BillingDataSource(ctx context.Context) (string, error) {
	var source string
	err := s.db.QueryRowContext(ctx, `SELECT data_source FROM billing_configuration WHERE id=1`).Scan(&source)
	if err == sql.ErrNoRows {
		return "source", nil
	}
	return source, err
}
func (s Store) SetBillingDataSource(ctx context.Context, source, actor string) error {
	if source != "source" && source != "archive" {
		return fmt.Errorf("invalid billing source")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO billing_configuration(id,data_source,updated_by,updated_at) VALUES(1,?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE data_source=VALUES(data_source),updated_by=VALUES(updated_by),updated_at=VALUES(updated_at)`, source, actor)
	return err
}
