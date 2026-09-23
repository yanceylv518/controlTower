package archivejob

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

type row map[string]*string

func (r row) text(k string) string {
	if r[k] == nil {
		return ""
	}
	return *r[k]
}
func (r row) number(k string) (int64, error) { return strconv.ParseInt(r.text(k), 10, 64) }
func rowHash(r row) string {
	h := sha256.New()
	keys := make([]string, 0, len(r))
	for key := range r {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	field := func(value string) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		h.Write(size[:])
		h.Write([]byte(value))
	}
	for _, key := range keys {
		field(key)
		if r[key] == nil {
			h.Write([]byte{0})
		} else {
			h.Write([]byte{1})
			field(*r[key])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
func chain(previous string, r row) string {
	h := sha256.Sum256([]byte(previous + rowHash(r)))
	return hex.EncodeToString(h[:])
}
func readRows(ctx context.Context, db queryer, query string, args ...any) ([]row, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []row{}
	totalBytes := 0
	for rows.Next() {
		cells := make([]sql.NullString, len(cols))
		dest := make([]any, len(cols))
		for i := range cells {
			dest[i] = &cells[i]
		}
		if err = rows.Scan(dest...); err != nil {
			return nil, err
		}
		r := row{}
		for i, col := range cols {
			if cells[i].Valid {
				v := cells[i].String
				totalBytes += len(v)
				if totalBytes > 8*1024*1024 {
					return nil, errors.New("batch_payload_limit_reduce_batch_size")
				}
				r[col] = &v
			} else {
				r[col] = nil
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (e *Engine) ensureMonth(ctx context.Context, c *sql.Conn, name string) error {
	if !monthName.MatchString(name) {
		return errors.New("invalid_month_table")
	}
	var exists int
	if err := c.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", name).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	var ignored, ddl string
	if err := e.source.QueryRowContext(ctx, "SHOW CREATE TABLE logs").Scan(&ignored, &ddl); err != nil {
		return err
	}
	if !strings.HasPrefix(ddl, "CREATE TABLE `logs` ") || strings.Contains(ddl, "FOREIGN KEY") {
		return errors.New("unsupported_source_table_template")
	}
	ddl = strings.Replace(ddl, "CREATE TABLE `logs` ", "CREATE TABLE "+q(name)+" ", 1)
	_, err := c.ExecContext(ctx, ddl)
	return err
}

func (e *Engine) collect(ctx context.Context, c *sql.Conn, s *state, batch, delay int) error {
	if batch < 1 || batch > 5000 {
		return errors.New("invalid_batch_size")
	}
	// Do not filter out recent IDs and then advance beyond them: stop at the
	// first row inside the delay window, otherwise a delayed ID could be skipped.
	rows, err := readRows(ctx, e.source, "SELECT /*+ MAX_EXECUTION_TIME(3000) */ * FROM logs WHERE id>? ORDER BY id LIMIT ?", s.Collection.AfterID, batch)
	if err != nil {
		return err
	}
	cutoff := time.Now().Unix() - int64(delay)
	accepted := rows[:0]
	for _, r := range rows {
		created, err := r.number("created_at")
		if err != nil {
			return errors.New("invalid_log_created_at")
		}
		if created > cutoff {
			break
		}
		accepted = append(accepted, r)
	}
	before := s.Collection.AfterID
	for _, r := range accepted {
		created, _ := r.number("created_at")
		if err = e.ensureMonth(ctx, c, table(day(created))); err != nil {
			return err
		}
	}
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	inserted, changed, unchanged := 0, 0, 0
	for _, r := range accepted {
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
		n, err := r.number("id")
		if err != nil {
			return errors.New("invalid_log_id")
		}
		s.Collection.AfterID = n
	}
	s.Collection.Rows += uint64(len(accepted))
	s.Collection.Step = "collect"
	s.Collection.Error = ""
	s.Collection.UpdatedAt = time.Now().UTC()
	if len(accepted) > 0 {
		last := accepted[len(accepted)-1]
		created, _ := last.number("created_at")
		s.Collection.Date = day(created)
		s.Collection.Table = table(s.Collection.Date)
	}
	if err = receipt(ctx, tx, "collection", before, s.Collection.AfterID, len(accepted), inserted, changed, unchanged); err != nil {
		return err
	}
	if err = save(ctx, tx, *s); err != nil {
		return err
	}
	return tx.Commit()
}
func receipt(ctx context.Context, tx *sql.Tx, task string, before, after int64, read, inserted, changed, unchanged int) error {
	if read == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO log_archive_batches VALUES(?,?,?,?,?,?,?,?,UTC_TIMESTAMP(6))", id(), task, before, after, read, inserted, changed, unchanged)
	return err
}
func writeRaw(ctx context.Context, tx *sql.Tx, r row, s *state) (bool, bool, error) {
	n, err := r.number("id")
	if err != nil || n < 0 {
		return false, false, errors.New("invalid_log_id")
	}
	created, err := r.number("created_at")
	if err != nil {
		return false, false, errors.New("invalid_log_created_at")
	}
	date := day(created)
	name := table(date)
	cols := make([]string, 0, len(r))
	for k := range r {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	quoted := make([]string, len(cols))
	values := make([]any, len(cols))
	marks := make([]string, len(cols))
	updates := make([]string, 0, len(cols))
	for i, k := range cols {
		quoted[i] = q(k)
		marks[i] = "?"
		if r[k] != nil {
			values[i] = *r[k]
		}
		if k != "id" {
			updates = append(updates, q(k)+"=VALUES("+q(k)+")")
		}
	}
	existing, err := readRows(ctx, tx, "SELECT "+strings.Join(quoted, ",")+" FROM "+q(name)+" WHERE id=? FOR UPDATE", n)
	if err != nil {
		return false, false, err
	}
	inserted := len(existing) == 0
	changed := inserted || rowHash(existing[0]) != rowHash(r)
	if changed {
		if _, err = tx.ExecContext(ctx, "INSERT INTO "+q(name)+" ("+strings.Join(quoted, ",")+") VALUES ("+strings.Join(marks, ",")+") ON DUPLICATE KEY UPDATE "+strings.Join(updates, ","), values...); err != nil {
			return false, false, err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO log_archive_days(log_date,revision,state,updated_at) VALUES(?,1,'pending',UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE log_date=VALUES(log_date)", date); err != nil {
		return false, false, err
	}
	if changed {
		if _, err = tx.ExecContext(ctx, "UPDATE log_archive_days SET revision=revision+1,state='pending',version_id='',raw_rows=NULL,error_code='',updated_at=UTC_TIMESTAMP(6) WHERE log_date=?", date); err != nil {
			return false, false, err
		}
	}
	if date > s.Frontier {
		s.Frontier = date
	}
	if s.FirstDate == "" || date < s.FirstDate {
		s.FirstDate = date
		s.FirstDateSource = "archive"
	}
	return inserted, changed && !inserted, nil
}
