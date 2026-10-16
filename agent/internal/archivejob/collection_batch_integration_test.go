package archivejob

import (
	"context"
	aj "controltower/internal/archivejob"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Add per-statement latency and faults around a real MySQL connection, so the
// test measures transaction/data correctness as well as round-trip complexity.
type collectionTestConnector struct {
	driver.Connector
	mu      sync.Mutex
	queries []string
	delay   time.Duration
	failRaw error
	broken  bool
}

func (c *collectionTestConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &collectionTestConn{Conn: conn, owner: c}, nil
}

type collectionTestConn struct {
	driver.Conn
	owner *collectionTestConnector
}

func (c *collectionTestConn) before(ctx context.Context, query string) error {
	c.owner.mu.Lock()
	c.owner.queries = append(c.owner.queries, query)
	broken := c.owner.broken
	var fail error
	if strings.HasPrefix(query, "INSERT INTO `logs_") && c.owner.failRaw != nil {
		fail = c.owner.failRaw
		c.owner.failRaw = nil
		c.owner.broken = true
	}
	c.owner.mu.Unlock()
	if broken {
		return driver.ErrBadConn
	}
	if fail != nil {
		return fail
	}
	if c.owner.delay > 0 {
		timer := time.NewTimer(c.owner.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (c *collectionTestConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.before(ctx, query); err != nil {
		return nil, err
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}
func (c *collectionTestConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.before(ctx, query); err != nil {
		return nil, err
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}
func (c *collectionTestConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}
func instrumentCollection(t *testing.T, e *Engine) (*collectionTestConnector, *sql.DB) {
	t.Helper()
	base := e.target
	var name string
	if err := base.QueryRow("SELECT DATABASE()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	cfg, err := mysql.ParseDSN(os.Getenv("CT_ARCHIVE_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName, cfg.ParseTime = name, false
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	trace := &collectionTestConnector{Connector: connector}
	e.target = sql.OpenDB(trace)
	e.target.SetMaxOpenConns(2)
	t.Cleanup(func() { e.target.Close(); e.target = base })
	return trace, base
}
func seedCollectionThousand(t *testing.T, e *Engine, ctx context.Context) {
	t.Helper()
	// Initialize an empty archive before source records arrive.
	if _, err := e.Step(ctx, aj.Settings{}, 1000, 60, true); err != nil {
		t.Fatal(err)
	}
	start, _ := dateBounds("2026-01-31")
	var args []any
	var marks []string
	for i := 1; i <= 1000; i++ {
		marks = append(marks, "(?,?,2,7,199,'m',10,5,53,?)")
		at := start + int64(i)
		if i > 500 {
			at += 86400
		}
		args = append(args, i, at, `{"model_ratio":10,"user_model_discount":0.56,"quota_before_discount":95,"quota_after_discount":53}`)
	}
	if _, err := e.source.ExecContext(ctx, "INSERT INTO logs VALUES"+strings.Join(marks, ","), args...); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionThousandLatencyAndReplayMySQL(t *testing.T) {
	e, ctx := fixture(t)
	seedCollectionThousand(t, e, ctx)
	trace, base := instrumentCollection(t, e)
	trace.delay = 5 * time.Millisecond
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	begin := time.Now()
	st, err := e.Step(bounded, aj.Settings{Collection: true}, 1000, 60, true)
	if err != nil {
		t.Fatal(err)
	}
	if st.Collection.AfterID != 1000 || st.Collection.Rows != 1000 {
		t.Fatalf("incorrect cursor: %+v", st.Collection)
	}
	checks, inserts := 0, 0
	for _, query := range trace.queries {
		if strings.Contains(query, "SELECT COUNT(*) FROM information_schema.TABLES") {
			checks++
		}
		if strings.HasPrefix(query, "INSERT INTO `logs_") {
			inserts++
		}
	}
	if checks != 2 || inserts != 4 || len(trace.queries) > 150 {
		t.Fatalf("unbounded round trips: checks=%d inserts=%d total=%d", checks, inserts, len(trace.queries))
	}
	t.Logf("1000 rows, two months, 5ms injected per statement: %s, target statements=%d", time.Since(begin), len(trace.queries))
	for _, date := range []string{"2026-01-31", "2026-02-01"} {
		var revision int
		if err = base.QueryRowContext(ctx, "SELECT revision FROM log_archive_days WHERE log_date=?", date).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		if revision != 501 {
			t.Fatalf("revision=%d", revision)
		}
	}
	// Replay mixed unchanged/corrected rows through the existing exact comparison
	// path. A case-only change and NULL must not be lost to collation or coercion.
	trace.delay = 0
	if _, err = e.source.ExecContext(ctx, "UPDATE logs SET model_name='M',other=NULL WHERE id=999"); err != nil {
		t.Fatal(err)
	}
	err = e.locked(ctx, func(c *sql.Conn) error {
		s, err := load(ctx, c)
		if err != nil {
			return err
		}
		s.Collection.AfterID = 997
		return e.collect(ctx, c, &s, 1000, 60)
	})
	if err != nil {
		t.Fatal(err)
	}
	var model string
	var other sql.NullString
	if err = base.QueryRowContext(ctx, "SELECT model_name,other FROM logs_202602 WHERE id=999").Scan(&model, &other); err != nil {
		t.Fatal(err)
	}
	if model != "M" || other.Valid {
		t.Fatal("byte/NULL correction lost")
	}
	var inserted, changed, unchanged int
	if err = base.QueryRowContext(ctx, "SELECT inserted_rows,changed_rows,unchanged_rows FROM log_archive_batches WHERE after_id=1000 AND before_id=997").Scan(&inserted, &changed, &unchanged); err != nil {
		t.Fatal(err)
	}
	if inserted != 0 || changed != 1 || unchanged != 2 {
		t.Fatalf("incorrect receipt: %d %d %d", inserted, changed, unchanged)
	}
}

func TestCollectionChunkRollbackMySQL(t *testing.T) {
	e, ctx := fixture(t)
	seedCollectionThousand(t, e, ctx)
	if _, err := e.target.ExecContext(ctx, "CREATE TRIGGER reject_chunk BEFORE INSERT ON logs_202601 FOR EACH ROW BEGIN IF NEW.id=300 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected chunk failure'; END IF; END"); err != nil {
		t.Fatal(err)
	}
	st, err := e.Step(ctx, aj.Settings{Collection: true}, 1000, 60, true)
	if err == nil || st.Collection.AfterID != 0 {
		t.Fatalf("failed batch advanced: %+v %v", st.Collection, err)
	}
	for _, table := range []string{"logs_202601", "log_archive_batches", "log_archive_days"} {
		var count int
		if err = e.target.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+q(table)).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("partial commit in %s: %d", table, count)
		}
	}
	if _, err = e.target.ExecContext(ctx, "DROP TRIGGER reject_chunk"); err != nil {
		t.Fatal(err)
	}
	e.ready = false
	st, err = e.Step(ctx, aj.Settings{Collection: true}, 1000, 60, true)
	if err != nil || st.Collection.AfterID != 1000 {
		t.Fatalf("retry: %+v %v", st.Collection, err)
	}
}

func TestCollectionOriginalErrorSurvivesBrokenReadbackMySQL(t *testing.T) {
	for _, original := range []error{context.DeadlineExceeded, &mysql.MySQLError{Number: 1205, SQLState: [5]byte{'H', 'Y', '0', '0', '0'}, Message: "original lock timeout"}} {
		t.Run(code(original), func(t *testing.T) {
			e, ctx := fixture(t)
			seedCollectionThousand(t, e, ctx)
			trace, base := instrumentCollection(t, e)
			trace.failRaw = original
			st, err := e.Step(ctx, aj.Settings{Collection: true}, 1000, 60, true)
			if !errors.Is(err, original) || st.Collection.Error != code(original) {
				t.Fatalf("original replaced: %v, status=%s", err, st.Collection.Error)
			}
			s, err := load(ctx, base)
			if err != nil || s.Collection.AfterID != 0 || s.Collection.Rows != 0 {
				t.Fatalf("failed cursor persisted: %+v %v", s.Collection, err)
			}
		})
	}
}
