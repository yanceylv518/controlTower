package logarchive

import (
	"context"
	af "controltower/internal/archivecontract"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CalendarOrigin runs between data passes on the Agent poller. Source fallback
// is allowed only after every archive month is confirmed empty, never on errors.
func (w *Worker) CalendarOrigin(ctx context.Context) (*af.CalendarOrigin, error) {
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
	var earliest *int64
	for _, table := range tables {
		if !writerMonthlyName.MatchString(table) {
			return nil, fmt.Errorf("invalid monthly table")
		}
		if _, err := time.Parse("200601", table[5:]); err != nil {
			return nil, err
		}
		timestamp, err := firstLogTimestamp(ctx, w.target, table)
		if err != nil {
			return nil, err
		}
		if timestamp != nil && (earliest == nil || *timestamp < *earliest) {
			earliest = timestamp
		}
	}
	origin := &af.CalendarOrigin{Source: "archive", ObservedAt: time.Now().UTC()}
	if earliest == nil {
		earliest, err = firstLogTimestamp(ctx, w.source, "logs")
		if err != nil {
			return nil, err
		}
		origin.Source = "source"
	}
	if earliest == nil {
		origin.Source = "empty"
	} else {
		origin.Date = time.Unix(*earliest, 0).In(archiveLocation).Format("2006-01-02")
	}
	return origin, nil
}

func firstLogTimestamp(ctx context.Context, db *sql.DB, table string) (*int64, error) {
	var exists int
	err := db.QueryRowContext(ctx, `SELECT /*+ MAX_EXECUTION_TIME(1000) */ 1 FROM `+quote(table)+` LIMIT 1`).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var index string
	err = db.QueryRowContext(ctx, `SELECT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND SEQ_IN_INDEX=1 AND COLUMN_NAME='created_at' AND SUB_PART IS NULL AND INDEX_TYPE='BTREE' ORDER BY INDEX_NAME LIMIT 1`, table).Scan(&index)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("calendar_origin_timestamp_index_missing")
	}
	if err != nil {
		return nil, err
	}
	var timestamp int64
	err = db.QueryRowContext(ctx, `SELECT /*+ MAX_EXECUTION_TIME(1000) */ created_at FROM `+quote(table)+` FORCE INDEX (`+quote(index)+`) ORDER BY created_at LIMIT 1`).Scan(&timestamp)
	if err != nil {
		return nil, err
	}
	return &timestamp, nil
}
