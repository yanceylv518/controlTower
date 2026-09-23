package logarchive

import (
	"context"
	af "controltower/internal/archivecontract"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// LatestRawPosition reads one primary-key endpoint per monthly table. Old imported
// logs are included; this is neither the collection checkpoint nor proof of coverage.
func (w *Worker) LatestRawPosition(ctx context.Context) (*af.RawPosition, error) {
	rows, err := w.target.QueryContext(ctx, `SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME REGEXP '^logs_[0-9]{6}$' ORDER BY TABLE_NAME`)
	if err != nil {
		return nil, err
	}
	var tables []string
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			rows.Close()
			return nil, err
		}
		tables = append(tables, table)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	position := &af.RawPosition{ObservedAt: time.Now().UTC()}
	for _, table := range tables {
		if !writerMonthlyName.MatchString(table) {
			return nil, fmt.Errorf("invalid monthly table")
		}
		if _, err := time.Parse("200601", table[5:]); err != nil {
			return nil, err
		}
		var id, created int64
		err = w.target.QueryRowContext(ctx, `SELECT /*+ MAX_EXECUTION_TIME(1000) */ id,created_at FROM `+quote(table)+` FORCE INDEX (PRIMARY) ORDER BY id DESC LIMIT 1`).Scan(&id, &created)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if position.Table == "" || id > position.ID {
			t := time.Unix(created, 0).UTC()
			position.Table, position.ID, position.LogTime = table, id, &t
		}
	}
	return position, nil
}
