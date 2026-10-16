package archivejob

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
)

const collectionWriteRows = 256
const collectionStatementBytes = 1 << 20
const collectionStatementParams = 60000

// The normal collection path contains new IDs. Lock/check IDs by month, then
// insert them in bounded statements. Existing IDs still use the byte-exact
// comparison and correction path, including oversized old-row invalidation.
// Everything, including revisions, live deltas and the caller's checkpoint,
// belongs to the same transaction; a failed chunk cannot advance the cursor.
func writeCollectionBatch(ctx context.Context, tx *sql.Tx, records []row, s *state) (inserted, changed, unchanged int, err error) {
	months := map[string][]row{}
	for _, r := range records {
		n, idErr := r.number("id")
		if idErr != nil || n < 0 {
			return 0, 0, 0, errors.New("invalid_log_id")
		}
		created, timeErr := r.number("created_at")
		if timeErr != nil {
			return 0, 0, 0, errors.New("invalid_log_created_at")
		}
		name := table(day(created))
		months[name] = append(months[name], r)
	}
	names := make([]string, 0, len(months))
	for name := range months {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		page := months[name]
		args := make([]any, len(page))
		for i, r := range page {
			args[i], _ = r.number("id")
		}
		rows, queryErr := tx.QueryContext(ctx, "SELECT id FROM "+q(name)+" WHERE id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+") ORDER BY id FOR UPDATE", args...)
		if queryErr != nil {
			return 0, 0, 0, queryErr
		}
		existing := map[int64]bool{}
		for rows.Next() {
			var n int64
			if err = rows.Scan(&n); err != nil {
				rows.Close()
				return 0, 0, 0, err
			}
			existing[n] = true
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return 0, 0, 0, err
		}
		if closeErr != nil {
			return 0, 0, 0, closeErr
		}
		fresh := make([]row, 0, len(page))
		for _, r := range page {
			n, _ := r.number("id")
			if !existing[n] {
				fresh = append(fresh, r)
				continue
			}
			a, b, writeErr := writeRaw(ctx, tx, r, s)
			if writeErr != nil {
				return 0, 0, 0, writeErr
			}
			if a {
				inserted++
			} else if b {
				changed++
			} else {
				unchanged++
			}
		}
		if err = insertCollectionRows(ctx, tx, name, fresh); err != nil {
			return 0, 0, 0, err
		}
		inserted += len(fresh)
		days := map[string]int{}
		for _, r := range fresh {
			created, _ := r.number("created_at")
			days[day(created)]++
			s.liveChanges = append(s.liveChanges, rawChange{after: r})
			n, _ := r.number("id")
			if s.LargeLive != nil && s.LargeLive.Table == name && s.LargeLive.ID == n {
				s.LargeLive = nil
			}
		}
		dates := make([]string, 0, len(days))
		for date := range days {
			dates = append(dates, date)
		}
		sort.Strings(dates)
		for _, date := range dates {
			if err = touchRawDayCount(ctx, tx, s, date, days[date]); err != nil {
				return 0, 0, 0, err
			}
		}
	}
	return inserted, changed, unchanged, nil
}

func insertCollectionRows(ctx context.Context, tx *sql.Tx, name string, records []row) error {
	if len(records) == 0 {
		return nil
	}
	cols := make([]string, 0, len(records[0]))
	for col := range records[0] {
		cols = append(cols, col)
	}
	sort.Strings(cols)
	quoted := make([]string, len(cols))
	for i, col := range cols {
		quoted[i] = q(col)
	}
	prefix := "INSERT INTO " + q(name) + " (" + strings.Join(quoted, ",") + ") VALUES "
	mark := "(" + strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",") + ")"
	var values []string
	var args []any
	bytes := len(prefix)
	flush := func() error {
		if len(values) == 0 {
			return nil
		}
		// A concurrent unexpected insert must fail and roll back, never silently
		// overwrite data whose live-stat delta was not read under this transaction.
		_, err := tx.ExecContext(ctx, prefix+strings.Join(values, ","), args...)
		values, args, bytes = nil, nil, len(prefix)
		return err
	}
	for _, r := range records {
		if len(r) != len(cols) {
			return errors.New("archive_column_mismatch")
		}
		rowBytes := len(mark) + 1 + len(cols)*16
		for _, col := range cols {
			v, ok := r[col]
			if !ok {
				return errors.New("archive_column_mismatch")
			}
			if v != nil {
				rowBytes += 2 * len(*v) // Also bound escaped parameter interpolation.
			}
		}
		if len(values) >= collectionWriteRows || len(args)+len(cols) > collectionStatementParams || bytes+rowBytes > collectionStatementBytes {
			if err := flush(); err != nil {
				return err
			}
		}
		// A large single row travels alone, subject to the existing page/row caps.
		values = append(values, mark)
		for _, col := range cols {
			var value any
			if r[col] != nil {
				value = *r[col]
			}
			args = append(args, value)
		}
		bytes += rowBytes
	}
	return flush()
}
