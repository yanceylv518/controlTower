// Package archivejob implements only continuous collection and daily history.
// No old archive tables, migrations, contribution ledgers or task states are read.
package archivejob

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	aj "controltower/internal/archivejob"
	"github.com/go-sql-driver/mysql"
)

//go:embed schema.sql
var schema string
var beijing = time.FixedZone("Asia/Shanghai", 28800)
var monthName = regexp.MustCompile(`^logs_[0-9]{6}$`)

type Engine struct {
	site           string
	source, target *sql.DB
	identity       string
	template       string
	ready          bool
	pageAfter      string
	counts         map[string]*dayCount
	countAfter     string
	countDate      string
	countError     string
}

func (e *Engine) BindSite(site string) error {
	if site == "" || len(site) > 64 {
		return errors.New("archive_site_required")
	}
	if e.site != "" {
		if e.site != site {
			return errors.New("archive_site_changed")
		}
		return nil
	}
	if e.ready {
		return errors.New("archive_site_binding_too_late")
	}
	h := sha256.Sum256([]byte(e.identity + "\x00" + site))
	e.identity = hex.EncodeToString(h[:])
	e.site = site
	return nil
}

type history struct {
	AfterCreated int64
	Date         string
	Step         string
	AfterID      int64
	Revision     uint64
	Version      string
	SourceHash   string
	TargetHash   string
	SourceRows   uint64
	TargetRows   uint64
}
type state struct {
	SeedFrom           string
	SeedThrough        string
	Collection         aj.Progress
	History            history
	HistoryProgress    aj.Progress
	FirstDate          string
	FirstDateSource    string
	Frontier           string
	RetryToken         string
	HistoryTurns       int
	ScheduleCollection int
	ScheduleHistory    int
	Turns              int
}

func Open(sourceDSN, targetDSN string) (*Engine, error) {
	a, err := mysql.ParseDSN(sourceDSN)
	if err != nil {
		return nil, errors.New("source_dsn_invalid")
	}
	b, err := mysql.ParseDSN(targetDSN)
	if err != nil {
		return nil, errors.New("archive_dsn_invalid")
	}
	if a.DBName == "" || b.DBName == "" || (a.Net == b.Net && a.Addr == b.Addr && a.DBName == b.DBName) {
		return nil, errors.New("archive_requires_separate_database")
	}
	for _, c := range []*mysql.Config{a, b} {
		c.ParseTime = false
		c.MultiStatements = false
		c.Timeout = 5 * time.Second
		c.ReadTimeout = 20 * time.Second
		c.WriteTimeout = 20 * time.Second
		if c.Params == nil {
			c.Params = map[string]string{}
		}
		c.Params["time_zone"] = "'+00:00'"
	}
	src, err := sql.Open("mysql", a.FormatDSN())
	if err != nil {
		return nil, err
	}
	dst, err := sql.Open("mysql", b.FormatDSN())
	if err != nil {
		src.Close()
		return nil, err
	}
	src.SetMaxOpenConns(1)
	dst.SetMaxOpenConns(2)
	hash := sha256.Sum256([]byte(a.Net + "\x00" + a.Addr + "\x00" + a.DBName))
	return &Engine{source: src, target: dst, identity: hex.EncodeToString(hash[:])}, nil
}
func (e *Engine) Close() { e.source.Close(); e.target.Close() }
func q(s string) string  { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }
func id() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func day(t int64) string       { return time.Unix(t, 0).In(beijing).Format("2006-01-02") }
func table(date string) string { return "logs_" + strings.ReplaceAll(date[:7], "-", "") }
func code(err error) string {
	if err == nil {
		return ""
	}
	var m *mysql.MySQLError
	if errors.As(err, &m) {
		return fmt.Sprintf("mysql_%d_%s", m.Number, m.SQLState)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "database_timeout"
	}
	s := err.Error()
	if len(s) > 128 {
		return "archive_operation_failed"
	}
	return s
}

// One connection-owned lock covers initialization and each bounded batch. Source
// reads cannot overlap between collection and history, even across Agent sessions.
func (e *Engine) locked(ctx context.Context, fn func(*sql.Conn) error) error {
	c, err := e.target.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	var name string
	if err = c.QueryRowContext(ctx, "SELECT CONCAT('log_archive:',SHA2(DATABASE(),256))").Scan(&name); err != nil {
		return err
	}
	name = name[:60]
	var got sql.NullInt64
	if err = c.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", name).Scan(&got); err != nil {
		return err
	}
	if !got.Valid || got.Int64 != 1 {
		return errors.New("archive_writer_busy")
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = c.ExecContext(release, "DO RELEASE_LOCK(?)", name)
	}()
	return fn(c)
}

func (e *Engine) prepare(ctx context.Context, c *sql.Conn) error {
	if e.ready {
		return nil
	}
	for _, ddl := range strings.Split(schema, ";") {
		if strings.TrimSpace(ddl) != "" {
			if _, err := c.ExecContext(ctx, ddl); err != nil {
				return err
			}
		}
	}
	var sourceHash string
	var version int
	err := c.QueryRowContext(ctx, "SELECT source_hash,schema_version FROM log_archive_meta WHERE singleton_id=1").Scan(&sourceHash, &version)
	if err == nil {
		if sourceHash != e.identity || version != 1 {
			return errors.New("archive_identity_or_schema_mismatch")
		}
		e.ready = true
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	names, err := months(ctx, c)
	if err != nil {
		return err
	}
	s := state{Collection: aj.Progress{Step: "idle", UpdatedAt: time.Now().UTC()}, FirstDateSource: "archive"}
	for _, name := range names {
		var max sql.NullInt64
		if err = c.QueryRowContext(ctx, "SELECT MAX(id) FROM "+q(name)).Scan(&max); err != nil {
			return err
		}
		if !max.Valid {
			continue
		}
		if max.Int64 > s.Collection.AfterID {
			s.Collection.AfterID = max.Int64
		}
		first, last, err := timeBounds(ctx, c, name)
		if err != nil {
			return err
		}
		if s.FirstDate == "" || first < s.FirstDate {
			s.FirstDate = first
		}
		if last > s.Frontier {
			s.Frontier = last
		}
	}
	if s.FirstDate == "" {
		first, _, err := timeBounds(ctx, e.source, "logs")
		if err != nil {
			return err
		}
		s.FirstDate = first
		s.FirstDateSource = "source"
	}
	raw, _ := json.Marshal(s)
	_, err = c.ExecContext(ctx, "INSERT INTO log_archive_meta VALUES(1,1,?,?,UTC_TIMESTAMP(6))", e.identity, string(raw))
	e.ready = err == nil
	return err
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func months(ctx context.Context, db queryer) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME REGEXP '^logs_[0-9]{6}$' ORDER BY TABLE_NAME")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		if !monthName.MatchString(name) {
			return nil, errors.New("invalid_month_table")
		}
		if _, err = time.Parse("200601", name[5:]); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}
func timeBounds(ctx context.Context, db queryer, name string) (string, string, error) {
	var exists int
	err := db.QueryRowContext(ctx, "SELECT 1 FROM "+q(name)+" LIMIT 1").Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	var index string
	err = db.QueryRowContext(ctx, "SELECT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND SEQ_IN_INDEX=1 AND COLUMN_NAME='created_at' AND INDEX_TYPE='BTREE' LIMIT 1", name).Scan(&index)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", errors.New("created_at_index_required")
	}
	if err != nil {
		return "", "", err
	}
	var a, b int64
	err = db.QueryRowContext(ctx, "SELECT /*+ MAX_EXECUTION_TIME(1500) */ MIN(created_at),MAX(created_at) FROM "+q(name)+" FORCE INDEX ("+q(index)+")").Scan(&a, &b)
	if err != nil {
		return "", "", err
	}
	return day(a), day(b), nil
}
func load(ctx context.Context, db queryer) (state, error) {
	var s state
	var b []byte
	err := db.QueryRowContext(ctx, "SELECT state_json FROM log_archive_meta WHERE singleton_id=1").Scan(&b)
	if err == nil {
		err = json.Unmarshal(b, &s)
	}
	return s, err
}
func save(ctx context.Context, tx *sql.Tx, s state) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE log_archive_meta SET state_json=?,updated_at=UTC_TIMESTAMP(6) WHERE singleton_id=1", string(b))
	return err
}

// Step executes one scheduled batch; disabling either task gives the other all slots.
func (e *Engine) Step(ctx context.Context, settings aj.Settings, batch, delay int, immutable bool) (aj.Status, error) {
	if !settings.Valid() || batch < 1 || batch > 5000 || delay < 60 || delay > 86400 {
		return aj.Status{Protocol: aj.Protocol}, errors.New("invalid_archive_settings")
	}
	historyTurn := false
	err := e.locked(ctx, func(c *sql.Conn) error {
		if err := e.prepare(ctx, c); err != nil {
			return err
		}
		s, err := load(ctx, c)
		if err != nil {
			return err
		}
		if settings.RetryToken != s.RetryToken {
			if _, err = c.ExecContext(ctx, "UPDATE log_archive_days SET state='pending',error_code='' WHERE state='failed'"); err != nil {
				return err
			}
			s.RetryToken = settings.RetryToken
		}
		if err = seedDays(ctx, c, &s); err != nil {
			return err
		}
		historyTurn = s.nextHistoryTurn(settings)
		if historyTurn {
			err = e.historyStep(ctx, c, &s, batch, immutable)
		} else if settings.Collection {
			err = e.collect(ctx, c, &s, batch, delay)
		}
		if err != nil {
			p := &s.Collection
			if historyTurn {
				p = &s.HistoryProgress
			}
			p.Error = code(err)
			p.UpdatedAt = time.Now().UTC()
		}
		// On failure the batch transaction has rolled back. Persist only diagnostic
		// state; reload committed cursors so no failed write can advance the stream.
		if err != nil {
			committed, readErr := load(ctx, c)
			if readErr != nil {
				return readErr
			}
			if historyTurn {
				committed.HistoryProgress = s.HistoryProgress
			} else {
				committed.Collection.Error = s.Collection.Error
			}
			s = committed
		}
		tx, txErr := c.BeginTx(ctx, nil)
		if txErr != nil {
			return txErr
		}
		defer tx.Rollback()
		if txErr = save(ctx, tx, s); txErr != nil {
			return txErr
		}
		if txErr = tx.Commit(); txErr != nil {
			return txErr
		}
		return err
	})
	// The polling loop reports Refresh's snapshot. Reading a task result must
	// not advance the reporting cursor and skip every other page of days.
	st, readErr := e.status(ctx, false)
	if err != nil {
		if st.Collection.Error == "" && st.History.Error == "" {
			if historyTurn {
				st.History.Error = code(err)
			} else {
				st.Collection.Error = code(err)
			}
		}
		return st, err
	}
	return st, readErr
}

func (e *Engine) Status(ctx context.Context) (aj.Status, error) {
	return e.status(ctx, true)
}

func (e *Engine) status(ctx context.Context, advance bool) (aj.Status, error) {
	st := aj.Status{Protocol: aj.Protocol, Days: []aj.Day{}, CountsDate: e.countDate, CountsError: e.countError}
	if !e.ready {
		return st, nil
	}
	s, err := load(ctx, e.target)
	if err != nil {
		return st, err
	}
	st.Collection = s.Collection
	st.History = s.HistoryProgress
	st.FirstDate = s.FirstDate
	st.FirstDateSource = s.FirstDateSource
	st.Frontier = s.Frontier
	st.Cutoff = time.Now().In(beijing).AddDate(0, 0, -1).Format("2006-01-02")
	names, err := months(ctx, e.target)
	if err != nil {
		return st, err
	}
	for _, name := range names {
		var n, created int64
		err = e.target.QueryRowContext(ctx, "SELECT id,created_at FROM "+q(name)+" ORDER BY id DESC LIMIT 1").Scan(&n, &created)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return st, err
		}
		if st.Latest == nil || n > st.Latest.ID {
			st.Latest = &aj.Position{ID: n, Table: name, LogTime: time.Unix(created, 0).UTC(), ObservedAt: time.Now().UTC()}
		}
	}
	query := "SELECT CAST(log_date AS CHAR),state,revision,version_id,COALESCE(CAST(raw_rows AS CHAR),''),step,error_code FROM log_archive_days"
	var args []any
	if e.pageAfter != "" {
		query += " WHERE log_date>?"
		args = append(args, e.pageAfter)
	}
	rows, err := e.target.QueryContext(ctx, query+" ORDER BY log_date LIMIT 100", args...)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var d aj.Day
		if err = rows.Scan(&d.Date, &d.State, &d.Revision, &d.Version, &d.Rows, &d.Step, &d.Error); err != nil {
			return st, err
		}
		st.Days = append(st.Days, d)
	}
	if err = rows.Err(); err != nil {
		return st, err
	}
	rows.Close()
	if len(st.Days) == 100 {
		st.NextDay = st.Days[99].Date
	}
	if advance {
		e.pageAfter = st.NextDay
	}
	return st, rows.Err()
}

func seedDays(ctx context.Context, c *sql.Conn, s *state) error {
	if s.FirstDate == "" || s.Frontier == "" {
		return nil
	}
	first, err := time.Parse("2006-01-02", s.FirstDate)
	if err != nil {
		return err
	}
	if s.SeedThrough != "" && s.SeedFrom == s.FirstDate {
		seeded, _ := time.Parse("2006-01-02", s.SeedThrough)
		first = seeded.AddDate(0, 0, 1)
	}
	last := s.Frontier
	today := time.Now().In(beijing).Format("2006-01-02")
	if last > today {
		last = today
	}
	seeded := 0
	for date := first.Format("2006-01-02"); date <= last && seeded < 100; date = first.Format("2006-01-02") {
		state := "pending"
		if date >= s.Frontier || date == today {
			state = "collecting"
		}
		if _, err = c.ExecContext(ctx, "INSERT IGNORE INTO log_archive_days(log_date,revision,state,updated_at) VALUES(?,1,?,UTC_TIMESTAMP(6))", date, state); err != nil {
			return err
		}
		first = first.AddDate(0, 0, 1)
		seeded++
		s.SeedThrough = date
	}
	s.SeedFrom = s.FirstDate
	_, err = c.ExecContext(ctx, "UPDATE log_archive_days SET state='pending' WHERE state='collecting' AND log_date<? AND log_date<?", s.Frontier, today)
	if err == nil {
		_, err = c.ExecContext(ctx, "UPDATE log_archive_days SET state='collecting' WHERE state='pending' AND (log_date>=? OR log_date>=?)", s.Frontier, today)
	}
	return err
}
