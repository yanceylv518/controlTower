package archivejob

import (
	"context"
	"database/sql"
	"errors"
	"time"

	aj "controltower/internal/archivejob"
	"github.com/go-sql-driver/mysql"
)

// Counts are independent of the collection/history switches. Partial scans are
// kept separate from raw_rows, which only ever contains a complete exact count.
type dayCount struct {
	revision uint64
	created  int64
	id       int64
	rows     uint64
	started  bool
}

const countPageSize = 50000
const countPagesPerRefresh = 32

// Refresh updates reporting metadata and archive-only counts. It never collects
// source logs or advances history processing, including when both tasks pause.
func (e *Engine) Refresh(ctx context.Context) (aj.Status, error) {
	e.countError = ""
	err := e.locked(ctx, func(c *sql.Conn) error {
		if err := e.prepare(ctx, c); err != nil {
			return err
		}
		s, err := load(ctx, c)
		if err != nil {
			return err
		}
		if err = seedDays(ctx, c, &s); err != nil {
			return err
		}
		tx, err := c.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = save(ctx, tx, s); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		countCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		for i := 0; i < countPagesPerRefresh; i++ {
			found, err := e.countNextDay(countCtx, c, countPageSize)
			if countCtx.Err() != nil && ctx.Err() == nil {
				// This refresh used its work budget. Resume the committed scan
				// on the next refresh and still report statuses using ctx.
				return nil
			}
			if err != nil || !found {
				return err
			}
		}
		return nil
	})
	if err != nil {
		e.countError = code(err)
	}
	st, readErr := e.Status(ctx)
	if readErr != nil {
		st.CountsError = code(readErr)
		return st, readErr
	}
	return st, err
}

func (e *Engine) countNextDay(ctx context.Context, c *sql.Conn, batch int) (bool, error) {
	var date string
	var revision uint64
	readNext := func() error {
		query := "SELECT CAST(log_date AS CHAR),revision FROM log_archive_days WHERE raw_rows IS NULL"
		var args []any
		if e.countAfter != "" {
			query += " AND log_date>?"
			args = append(args, e.countAfter)
		}
		return c.QueryRowContext(ctx, query+" ORDER BY log_date LIMIT 1", args...).Scan(&date, &revision)
	}
	err := readNext()
	if errors.Is(err, sql.ErrNoRows) && e.countAfter != "" {
		e.countAfter = ""
		err = readNext()
	}
	if errors.Is(err, sql.ErrNoRows) {
		e.countDate = ""
		return false, nil
	}
	if err != nil {
		return false, err
	}
	e.countDate = date
	done, err := e.countDay(ctx, c, date, revision, batch)
	if done {
		e.countDate = ""
	}
	if done || (err != nil && ctx.Err() == nil) {
		// A broken date must not starve the remaining dates on every poll.
		e.countAfter = date
	}
	return true, err
}

func countQueryTimedOut(err error) bool {
	var dbErr *mysql.MySQLError
	return errors.As(err, &dbErr) && dbErr.Number == 3024
}

func (e *Engine) countDay(ctx context.Context, c *sql.Conn, date string, revision uint64, batch int) (bool, error) {
	if e.counts == nil {
		e.counts = map[string]*dayCount{}
	}
	progress := e.counts[date]
	if progress == nil || progress.revision != revision {
		progress = &dayCount{revision: revision}
		e.counts[date] = progress
	}
	name := table(date)
	var exists int
	if err := c.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", name).Scan(&exists); err != nil {
		return false, err
	}
	from, to := dateBounds(date)
	complete := exists == 0
	count := uint64(0)
	if exists != 0 {
		var index string
		var ordered bool
		// InnoDB appends the primary ID to a single-column time index. Prefer
		// that or (created_at,id) over (created_at,type), which needs a sort.
		err := c.QueryRowContext(ctx, `SELECT INDEX_NAME,
		 (COUNT(*)=1 OR MAX(SEQ_IN_INDEX=2 AND COLUMN_NAME='id')) AS ordered
		 FROM information_schema.STATISTICS
		 WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND INDEX_TYPE='BTREE' AND IS_VISIBLE='YES'
		 GROUP BY INDEX_NAME HAVING MAX(SEQ_IN_INDEX=1 AND COLUMN_NAME='created_at')
		 ORDER BY ordered DESC,INDEX_NAME LIMIT 1`, name).Scan(&index, &ordered)
		if errors.Is(err, sql.ErrNoRows) {
			return false, errors.New("archive_count_created_at_index_required")
		}
		if err != nil {
			return false, err
		}
		if !progress.started {
			err = c.QueryRowContext(ctx, "SELECT /*+ MAX_EXECUTION_TIME(500) */ COUNT(*) FROM "+q(name)+" FORCE INDEX ("+q(index)+") WHERE created_at>=? AND created_at<?", from, to).Scan(&count)
			if err == nil {
				complete = true
			} else if !countQueryTimedOut(err) {
				return false, err
			} else {
				progress.started = true
				progress.created = from
				progress.id = -1
			}
		}
		if !complete {
			if !ordered {
				return false, errors.New("archive_count_time_id_index_required")
			}
			// A time+ID cursor handles duplicate timestamps and IDs whose
			// chronological order differs from their numeric order.
			rows, err := c.QueryContext(ctx, "SELECT /*+ MAX_EXECUTION_TIME(1500) */ created_at,id FROM "+q(name)+" FORCE INDEX ("+q(index)+") WHERE created_at>=? AND created_at<? AND (created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?", from, to, progress.created, progress.created, progress.id, batch)
			if err != nil {
				return false, err
			}
			next := *progress
			n := 0
			for rows.Next() {
				if err = rows.Scan(&next.created, &next.id); err != nil {
					_ = rows.Close()
					return false, err
				}
				next.rows++
				n++
			}
			err = rows.Err()
			closeErr := rows.Close()
			if err != nil {
				return false, err
			}
			if closeErr != nil {
				return false, closeErr
			}
			*progress = next
			count = next.rows
			complete = n < batch
		}
	}
	if !complete {
		return false, nil
	}
	// Any archive write invalidates the revision. Never publish a count that
	// spans two revisions; the next pass starts again from the day boundary.
	_, err := c.ExecContext(ctx, "UPDATE log_archive_days SET raw_rows=? WHERE log_date=? AND revision=? AND raw_rows IS NULL", count, date, revision)
	if err != nil {
		return false, err
	}
	delete(e.counts, date)
	return true, nil
}
