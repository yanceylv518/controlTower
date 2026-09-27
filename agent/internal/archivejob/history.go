package archivejob

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func dateBounds(date string) (int64, int64) {
	d, _ := time.ParseInLocation("2006-01-02", date, beijing)
	return d.Unix(), d.AddDate(0, 0, 1).Unix()
}

func (e *Engine) historyStep(ctx context.Context, c *sql.Conn, s *state, batch int, immutable bool) error {
	cutoff := time.Now().In(beijing).AddDate(0, 0, -1).Format("2006-01-02")
	// Discover calendar days from raw-log boundaries, independent of old ledgers.
	// A day is eligible only after a committed later-day record exists.
	if s.History.Date == "" {
		if s.FirstDate == "" || s.Frontier == "" {
			return nil
		}

		var date string
		var revision uint64
		err := c.QueryRowContext(ctx, "SELECT CAST(log_date AS CHAR),revision FROM log_archive_days WHERE state IN ('pending','processing') AND log_date<=? AND log_date<? ORDER BY log_date LIMIT 1", cutoff, s.Frontier).Scan(&date, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		s.History = history{Date: date, Step: "verify_source", Revision: revision, Version: id()}
	}
	h := &s.History
	s.HistoryProgress.Date = h.Date
	s.HistoryProgress.Step = h.Step
	s.HistoryProgress.Table = table(h.Date)
	s.HistoryProgress.Error = ""
	s.HistoryProgress.UpdatedAt = time.Now().UTC()
	var revision uint64
	if err := c.QueryRowContext(ctx, "SELECT revision FROM log_archive_days WHERE log_date=?", h.Date).Scan(&revision); err != nil {
		return err
	}
	if h.Step != "verify_source" && revision != h.Revision {
		s.History = history{}
		return nil
	}
	switch h.Step {
	case "verify_source":
		return e.verifySource(ctx, c, s, batch)
	case "verify_archive":
		return e.verifyArchive(ctx, c, s, batch)
	case "summarize":
		return e.summarize(ctx, c, s, batch)
	case "seal":
		if !immutable {
			return failDay(ctx, c, s, "source_history_retention_unconfirmed")
		}
		tx, err := c.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var current uint64
		if err = tx.QueryRowContext(ctx, "SELECT revision FROM log_archive_days WHERE log_date=? FOR UPDATE", h.Date).Scan(&current); err != nil {
			return err
		}
		if current != h.Revision {
			s.History = history{}
			return nil
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO log_archive_day_versions VALUES(?,?,?,?,?,1,UTC_TIMESTAMP(6))", h.Version, h.Date, h.Revision, h.TargetRows, h.TargetHash); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE log_archive_days SET state='sealed',version_id=?,raw_rows=?,step='',error_code='',updated_at=UTC_TIMESTAMP(6) WHERE log_date=?", h.Version, h.TargetRows, h.Date); err != nil {
			return err
		}
		s.HistoryProgress.Step = "sealed"
		s.History = history{}
		if err = save(ctx, tx, *s); err != nil {
			return err
		}
		return tx.Commit()
	}
	return errors.New("invalid_history_step")
}
func failDay(ctx context.Context, c *sql.Conn, s *state, reason string) error {
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	h := s.History
	if _, err = tx.ExecContext(ctx, "UPDATE log_archive_days SET state='failed',step=?,error_code=?,updated_at=UTC_TIMESTAMP(6) WHERE log_date=?", h.Step, reason, h.Date); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO log_archive_issues VALUES(?,?,?,?,UTC_TIMESTAMP(6))", id(), h.Date, h.Step, reason); err != nil {
		return err
	}
	s.HistoryProgress.Error = reason
	s.HistoryProgress.Step = "failed"
	s.History = history{}
	if err = save(ctx, tx, *s); err != nil {
		return err
	}
	return tx.Commit()
}
func (e *Engine) verifySource(ctx context.Context, c *sql.Conn, s *state, batch int) error {
	h := &s.History
	var index string
	if err := e.source.QueryRowContext(ctx, "SELECT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='logs' AND SEQ_IN_INDEX=1 AND COLUMN_NAME='created_at' AND INDEX_TYPE='BTREE' LIMIT 1").Scan(&index); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("created_at_index_required")
		}
		return err
	}
	from, to := dateBounds(h.Date)
	rows, byteLimited, err := readPage(ctx, e.source, "SELECT /*+ MAX_EXECUTION_TIME(3000) */ * FROM logs WHERE created_at>=? AND created_at<? AND (created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?", from, to, h.AfterCreated, h.AfterCreated, h.AfterID, batch)
	if err != nil {
		return err
	}
	if err = e.ensureMonth(ctx, c, table(h.Date)); err != nil {
		return err
	}
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before := h.AfterID
	inserted, changed, unchanged := 0, 0, 0
	for _, r := range rows {
		a, b, err := writeRaw(ctx, tx, r, s)
		if err != nil {
			return err
		}
		if a {
			inserted++
		} else if b {
			changed++
		} else {
			unchanged++
		}
		h.SourceHash = chain(h.SourceHash, r)
		h.SourceRows++
		h.AfterID, _ = r.number("id")
		h.AfterCreated, _ = r.number("created_at")
	}
	if err = receipt(ctx, tx, "history", before, h.AfterID, len(rows), inserted, changed, unchanged); err != nil {
		return err
	}
	if !byteLimited && len(rows) < batch {
		h.Step = "verify_archive"
		h.AfterID = 0
		h.AfterCreated = 0
		if err = tx.QueryRowContext(ctx, "SELECT revision FROM log_archive_days WHERE log_date=?", h.Date).Scan(&h.Revision); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE log_archive_days SET state='processing',step=?,updated_at=UTC_TIMESTAMP(6) WHERE log_date=?", h.Step, h.Date); err != nil {
		return err
	}
	s.HistoryProgress.AfterID = h.AfterID
	s.HistoryProgress.Rows = h.SourceRows
	if err = save(ctx, tx, *s); err != nil {
		return err
	}
	return tx.Commit()
}
func (e *Engine) verifyArchive(ctx context.Context, c *sql.Conn, s *state, batch int) error {
	h := &s.History
	from, to := dateBounds(h.Date)
	rows, byteLimited, err := readPage(ctx, c, "SELECT /*+ MAX_EXECUTION_TIME(3000) */ * FROM "+q(table(h.Date))+" WHERE created_at>=? AND created_at<? AND (created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?", from, to, h.AfterCreated, h.AfterCreated, h.AfterID, batch)
	if err != nil {
		return err
	}
	for _, r := range rows {
		h.TargetHash = chain(h.TargetHash, r)
		h.TargetRows++
		h.AfterID, _ = r.number("id")
		h.AfterCreated, _ = r.number("created_at")
	}
	if !byteLimited && len(rows) < batch {
		if h.SourceRows != h.TargetRows || h.SourceHash != h.TargetHash {
			return failDay(ctx, c, s, "source_archive_content_mismatch")
		}
		h.Step = "summarize"
		h.AfterID = 0
		h.AfterCreated = 0
	}
	s.HistoryProgress.AfterID = h.AfterID
	s.HistoryProgress.Rows = h.TargetRows
	return nil
}
