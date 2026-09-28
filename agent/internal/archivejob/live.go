package archivejob

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
)

// Live statistics cover an explicitly checkpointed prefix of archive rows. They
// are independent of source verification and never constitute a sealed version.
type rawChange struct{ before, after row }
type liveCursor struct {
	version               string
	created, id           int64
	upperCreated, upperID int64
	ready                 bool
}

func (p liveCursor) covers(r row) bool {
	if r == nil || p.upperCreated < 0 {
		return false
	}
	if p.ready {
		return true
	}
	t, _ := r.number("created_at")
	id, _ := r.number("id")
	return t < p.created || t == p.created && id <= p.id || t > p.upperCreated || t == p.upperCreated && id > p.upperID
}
func delta(groups map[string]aggregate, r row, sign int64) error {
	key, a, err := parseAggregate(r)
	if err != nil {
		return err
	}
	old, ok := groups[key]
	if !ok {
		old = aggregate{Dimensions: a.Dimensions, Amounts: map[string]string{}}
	}
	for k, v := range a.Amounts {
		n, _ := new(big.Int).SetString(v, 10)
		n.Mul(n, big.NewInt(sign))
		prev := new(big.Int)
		if old.Amounts[k] != "" {
			prev.SetString(old.Amounts[k], 10)
		}
		old.Amounts[k] = prev.Add(prev, n).String()
	}
	groups[key] = old
	return nil
}

// Changes and checkpoint updates share the raw-write transaction. A replayed
// unchanged row produces no delta, including after a process restart.
func flushLive(ctx context.Context, tx *sql.Tx, s *state) error {
	changes := s.liveChanges
	if len(changes) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "SAVEPOINT live_statistics"); err != nil {
		return err
	}
	if err := applyLiveChanges(ctx, tx, s); err != nil {
		if _, rollbackErr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT live_statistics"); rollbackErr != nil {
			return rollbackErr
		}
		dates := map[string]bool{}
		for _, change := range changes {
			for _, r := range []row{change.before, change.after} {
				if r != nil {
					created, _ := r.number("created_at")
					dates[day(created)] = true
				}
			}
		}
		// Raw collection must not be held hostage by a malformed pricing record or
		// failed summary write. Invalidate the snapshot atomically, then rebuild it.
		for date := range dates {
			if _, resetErr := tx.ExecContext(ctx, `UPDATE log_archive_live_stats SET version_id=?,after_created=0,after_id=0,upper_created=-1,upper_id=0,ready=0,error_code=?,retry_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 60 SECOND),updated_at=UTC_TIMESTAMP(6) WHERE log_date=?`, id(), code(err), date); resetErr != nil {
				return resetErr
			}
		}
	}
	_, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT live_statistics")
	return err
}
func applyLiveChanges(ctx context.Context, tx *sql.Tx, s *state) error {
	changes := s.liveChanges
	s.liveChanges = nil
	byDate := map[string][]rawChange{}
	for _, change := range changes {
		if change.before != nil {
			t, _ := change.before.number("created_at")
			d := day(t)
			byDate[d] = append(byDate[d], rawChange{before: change.before})
		}
		if change.after != nil {
			t, _ := change.after.number("created_at")
			d := day(t)
			byDate[d] = append(byDate[d], rawChange{after: change.after})
		}
	}
	for date, changes := range byDate {
		var p liveCursor
		err := tx.QueryRowContext(ctx, "SELECT version_id,after_created,after_id,ready,upper_created,upper_id FROM log_archive_live_stats WHERE log_date=? FOR UPDATE", date).Scan(&p.version, &p.created, &p.id, &p.ready, &p.upperCreated, &p.upperID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		groups := map[string]aggregate{}
		for _, change := range changes {
			if p.covers(change.before) {
				if err = delta(groups, change.before, -1); err != nil {
					return err
				}
			}
			if p.covers(change.after) {
				if err = delta(groups, change.after, 1); err != nil {
					return err
				}
			}
		}
		if err = writeAggregateBatch(ctx, tx, p.version, date, groups); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE log_archive_live_stats SET updated_at=UTC_TIMESTAMP(6) WHERE log_date=?", date); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) liveStep(ctx context.Context, c *sql.Conn, batch int) error {
	s, err := load(ctx, c)
	if err != nil {
		return err
	}
	if s.LargeLive != nil {
		r := s.LargeLive
		var retry bool
		if err = c.QueryRowContext(ctx, "SELECT retry_at IS NULL OR retry_at<=UTC_TIMESTAMP(6) FROM log_archive_live_stats WHERE log_date=?", day(r.Created)).Scan(&retry); err != nil {
			return err
		}
		if retry {
			if err = e.processLarge(ctx, c, &s, r); err != nil {
				// A failed attempt may have mutated in-memory offsets. Reload the
				// committed checkpoint before recording diagnostics or discarding it.
				committed, readErr := load(ctx, c)
				if readErr != nil {
					return readErr
				}
				if largeSummaryDataError(err) {
					committed.LargeLive = nil
				}
				tx, saveErr := c.BeginTx(ctx, nil)
				if saveErr != nil {
					return saveErr
				}
				defer tx.Rollback()
				if _, saveErr = tx.ExecContext(ctx, "UPDATE log_archive_live_stats SET error_code=?,retry_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 60 SECOND) WHERE log_date=?", code(err), day(r.Created)); saveErr != nil {
					return saveErr
				}
				if saveErr = save(ctx, tx, committed); saveErr != nil {
					return saveErr
				}
				if saveErr = tx.Commit(); saveErr != nil {
					return saveErr
				}
			}
			return err
		}
		// During a transient failure's backoff, other dates can still build.
	}
	var date string
	// Newest incomplete dates first: today's statistics do not wait for history.
	err = c.QueryRowContext(ctx, `SELECT CAST(d.log_date AS CHAR) FROM log_archive_days d LEFT JOIN log_archive_live_stats l ON l.log_date=d.log_date WHERE (l.log_date IS NULL OR l.ready=0) AND (l.retry_at IS NULL OR l.retry_at<=UTC_TIMESTAMP(6)) ORDER BY d.log_date DESC LIMIT 1`).Scan(&date)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	err = e.buildLive(ctx, c, date, batch)
	var candidate *largeCandidate
	if errors.As(err, &candidate) {
		if s.LargeLive != nil {
			return nil
		} // Keep the first transfer's resumable state.
		if err = e.startLarge(ctx, c, &s, candidate, "live_summary"); err == nil {
			err = saveStandalone(ctx, c, s)
		}
	}
	if err != nil {
		_, _ = c.ExecContext(ctx, `UPDATE log_archive_live_stats SET error_code=?,retry_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 60 SECOND) WHERE log_date=?`, code(err), date)
	}
	return err
}
func (e *Engine) buildLive(ctx context.Context, c *sql.Conn, date string, batch int) error {
	if _, err := c.ExecContext(ctx, `INSERT IGNORE INTO log_archive_live_stats(log_date,version_id,upper_created,updated_at) VALUES(?,?,-1,UTC_TIMESTAMP(6))`, date, id()); err != nil {
		return err
	}
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var p liveCursor
	if err = tx.QueryRowContext(ctx, `SELECT version_id,after_created,after_id,ready,upper_created,upper_id FROM log_archive_live_stats WHERE log_date=? FOR UPDATE`, date).Scan(&p.version, &p.created, &p.id, &p.ready, &p.upperCreated, &p.upperID); err != nil {
		return err
	}
	from, to := dateBounds(date)
	if p.upperCreated < 0 {
		err = tx.QueryRowContext(ctx, "SELECT created_at,id FROM "+q(table(date))+" WHERE created_at>=? AND created_at<? ORDER BY created_at DESC,id DESC LIMIT 1", from, to).Scan(&p.upperCreated, &p.upperID)
		if errors.Is(err, sql.ErrNoRows) {
			p.upperCreated, p.upperID = 0, 0
		} else if err != nil {
			return err
		}
	}
	rows, limited, err := readArchivePage(ctx, tx, table(date), "SELECT /*+ MAX_EXECUTION_TIME(3000) */ * FROM "+q(table(date))+" WHERE created_at>=? AND created_at<? AND (created_at>? OR (created_at=? AND id>?)) AND (created_at<? OR (created_at=? AND id<=?)) ORDER BY created_at,id LIMIT ?", from, to, p.created, p.created, p.id, p.upperCreated, p.upperCreated, p.upperID, batch)
	var candidate *largeCandidate
	if errors.As(err, &candidate) {
		if _, saveErr := tx.ExecContext(ctx, "UPDATE log_archive_live_stats SET upper_created=?,upper_id=? WHERE log_date=?", p.upperCreated, p.upperID, date); saveErr != nil {
			return saveErr
		}
		if saveErr := tx.Commit(); saveErr != nil {
			return saveErr
		}
		return candidate
	}
	if err != nil {
		return err
	}
	groups := map[string]aggregate{}
	for _, r := range rows {
		if err = delta(groups, r, 1); err != nil {
			return err
		}
	}
	if err = writeAggregateBatch(ctx, tx, p.version, date, groups); err != nil {
		return err
	}
	if len(rows) > 0 {
		p.created, _ = rows[len(rows)-1].number("created_at")
		p.id, _ = rows[len(rows)-1].number("id")
	}
	_, err = tx.ExecContext(ctx, `UPDATE log_archive_live_stats SET after_created=?,after_id=?,ready=?,upper_created=?,upper_id=?,error_code='',retry_at=NULL,updated_at=UTC_TIMESTAMP(6) WHERE log_date=?`, p.created, p.id, !limited && len(rows) < batch, p.upperCreated, p.upperID, date)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func invalidateLive(ctx context.Context, tx *sql.Tx, date string) error {
	_, err := tx.ExecContext(ctx, "UPDATE log_archive_live_stats SET version_id=?,after_created=0,after_id=0,upper_created=-1,upper_id=0,ready=0,error_code='',retry_at=NULL,updated_at=UTC_TIMESTAMP(6) WHERE log_date=?", id(), date)
	return err
}
