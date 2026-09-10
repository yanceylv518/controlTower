package logarchive

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var testColumns = []string{"id", "content", "created_at", "type", "prompt_tokens", "completion_tokens", "quota"}

type testDB struct {
	source                                   bool
	noDateIndex                              bool
	failWrite, failCommit                    bool
	commits, rollbacks                       int
	after                                    int64
	writes                                   [][]driver.Value
	onCommit                                 func()
	ledger, pendingLedger                    map[int64]string
	stats, pendingStats                      map[string][]string
	queries                                  []string
	failStats                                bool
	sourceNow                                int64
	sourceRows                               [][]driver.Value
	verifyRows                               map[string]map[string][]driver.Value
	corruptVerification, missingVerification bool
}
type connector struct{ db *testDB }

func (c connector) Connect(context.Context) (driver.Conn, error) { return conn{c.db}, nil }
func (c connector) Driver() driver.Driver                        { return testDriver{} }

type testDriver struct{}

func (testDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unused") }

type conn struct{ db *testDB }

func (c conn) Close() error { return nil }
func (c conn) Begin() (driver.Tx, error) {
	c.db.verifyRows = map[string]map[string][]driver.Value{}
	c.db.pendingLedger = map[int64]string{}
	for k, v := range c.db.ledger {
		c.db.pendingLedger[k] = v
	}
	c.db.pendingStats = map[string][]string{}
	for k, v := range c.db.stats {
		c.db.pendingStats[k] = append([]string(nil), v...)
	}
	return transaction{c.db}, nil
}
func (c conn) Prepare(q string) (driver.Stmt, error) {
	if c.db.source || !strings.HasPrefix(q, "INSERT INTO logs") {
		return nil, errors.New("unexpected write")
	}
	return statement{c.db}, nil
}
func (c conn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.db.queries = append(c.db.queries, q)
	if c.db.source {
		return nil, errors.New("unexpected exec")
	}
	if strings.HasPrefix(q, "SET SESSION") || strings.HasPrefix(q, "CREATE TABLE") || strings.HasPrefix(q, "DELETE FROM") {
		return driver.RowsAffected(0), nil
	}
	if c.db.failWrite {
		return nil, errors.New("secret row details")
	}
	switch {
	case strings.HasPrefix(q, "INSERT INTO `logs_"):
		table := strings.Split(q, "`")[1]
		if c.db.verifyRows[table] == nil {
			c.db.verifyRows[table] = map[string][]driver.Value{}
		}
		for i := 0; i < len(args); i += 7 {
			row := []driver.Value{}
			for _, v := range args[i : i+7] {
				row = append(row, v.Value)
			}
			c.db.writes = append(c.db.writes, row)
			c.db.verifyRows[table][row[0].(string)] = row
		}
	case strings.HasPrefix(q, "INSERT INTO `archive_log_state`"):
		for i := 0; i < len(args); i += 2 {
			c.db.pendingLedger[args[i].Value.(int64)] = args[i+1].Value.(string)
		}
	case strings.HasPrefix(q, "INSERT INTO `log_daily_stats`") || strings.HasPrefix(q, "INSERT INTO `log_monthly_stats`"):
		if c.db.failStats {
			return nil, errors.New("stats failed")
		}
		key := strings.Split(q, "`")[1] + ":" + args[0].Value.(string)
		v := c.db.pendingStats[key]
		if v == nil {
			v = []string{"0", "0", "0", "0", "0", "0", "0"}
		}
		for i := range measureNames {
			a, _ := new(big.Int).SetString(v[i], 10)
			b, _ := new(big.Int).SetString(args[i+1].Value.(string), 10)
			v[i] = a.Add(a, b).String()
		}
		c.db.pendingStats[key] = v
	default:
		return nil, errors.New("unexpected exec")
	}
	return driver.RowsAffected(0), nil
}
func (c conn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(q, "ORDER BY created_at,id LIMIT"):
		out := result(testColumns)
		for _, row := range c.db.sourceRows {
			id, ts := row[0].(int64), row[2].(int64)
			if ts >= args[0].Value.(int64) && ts < args[1].Value.(int64) && (ts > args[2].Value.(int64) || ts == args[2].Value.(int64) && id > args[4].Value.(int64)) {
				out.rows = append(out.rows, row)
				if len(out.rows) >= int(args[5].Value.(int64)) {
					break
				}
			}
		}
		return out, nil
	case q == "SELECT * FROM logs LIMIT 0":
		return result(testColumns), nil
	case strings.HasPrefix(q, "SELECT INDEX_NAME FROM"):
		if c.db.noDateIndex {
			return result([]string{"index"}), nil
		}
		return result([]string{"index"}, []driver.Value{"idx_created_at"}), nil
	case strings.HasPrefix(q, "SELECT COUNT(*) FROM information_schema.TABLES"):
		return result([]string{"count"}, []driver.Value{int64(1)}), nil
	case strings.HasPrefix(q, "SELECT period_key,log_rows"):
		out := result([]string{"period_key", "log_rows", "request_rows", "error_rows"})
		for _, arg := range args {
			day := arg.Value.(string)
			if v, ok := c.db.pendingStats["log_daily_stats:"+day]; ok {
				out.rows = append(out.rows, []driver.Value{day, v[0], v[1], v[3]})
			}
		}
		return out, nil
	case strings.HasPrefix(q, "SELECT `id`,") && !c.db.source:
		table := strings.Split(strings.Split(q, " FROM ")[1], "`")[1]
		out := result(testColumns)
		for _, arg := range args {
			id, ok := arg.Value.(string)
			if !ok {
				id = strconv.FormatInt(arg.Value.(int64), 10)
			}
			if row, ok := c.db.verifyRows[table][id]; ok && !c.db.missingVerification {
				copyRow := append([]driver.Value(nil), row...)
				if c.db.corruptVerification {
					copyRow[1] = "tampered-secret"
				}
				out.rows = append(out.rows, copyRow)
			}
		}
		return out, nil
	case q == "SELECT UNIX_TIMESTAMP()":
		n := c.db.sourceNow
		if n == 0 {
			n = time.Now().Unix()
		}
		return result([]string{"now"}, []driver.Value{n}), nil
	case strings.HasPrefix(q, "SELECT id, contribution"):
		out := result([]string{"id", "contribution"})
		for _, arg := range args {
			id := arg.Value.(int64)
			if raw, ok := c.db.pendingLedger[id]; ok {
				out.rows = append(out.rows, []driver.Value{id, raw})
			}
		}
		return out, nil
	case strings.Contains(q, "@@server_uuid"):
		id := "target"
		if c.db.source {
			id = "source"
		}
		return result([]string{"uuid", "db"}, []driver.Value{id, "newapi"}), nil
	case strings.Contains(q, "information_schema.TABLES"):
		return result([]string{"engine"}, []driver.Value{"InnoDB"}), nil
	case strings.Contains(q, "information_schema.STATISTICS"):
		return result([]string{"index", "column"}, []driver.Value{"PRIMARY", "id"}), nil
	case strings.Contains(q, "information_schema.COLUMNS"):
		return result([]string{"name", "type", "nullable", "charset", "collation", "extra"}, []driver.Value{"id", "bigint", "NO", "", "", "auto_increment"}, []driver.Value{"content", "text", "YES", "utf8mb4", "utf8mb4_bin", ""}), nil
	case strings.HasPrefix(q, "SELECT * FROM logs") && c.db.source:
		c.db.after = args[0].Value.(int64)
		if c.db.sourceRows != nil {
			out := result(testColumns)
			for _, row := range c.db.sourceRows {
				if row[0].(int64) > c.db.after {
					out.rows = append(out.rows, row)
				}
			}
			return out, nil
		}
		if c.db.after >= 2 {
			return result(testColumns), nil
		}
		return result(testColumns, []driver.Value{int64(1), nil, int64(1788278400), int64(2), int64(10), int64(20), int64(30)}, []driver.Value{int64(2), "中文 ' secret", int64(1788278401), int64(5), int64(1), int64(2), int64(3)}), nil
	default:
		return nil, errors.New("unexpected query")
	}
}

type transaction struct{ db *testDB }

func (t transaction) Commit() error {
	if t.db.onCommit != nil {
		t.db.onCommit()
	}
	if t.db.failCommit {
		return errors.New("commit lost")
	}
	t.db.commits++
	t.db.ledger = t.db.pendingLedger
	t.db.stats = t.db.pendingStats
	return nil
}
func (t transaction) Rollback() error { t.db.rollbacks++; return nil }

type statement struct{ db *testDB }

func (s statement) Close() error                              { return nil }
func (s statement) NumInput() int                             { return -1 }
func (s statement) Query([]driver.Value) (driver.Rows, error) { return nil, errors.New("unused") }
func (s statement) Exec(v []driver.Value) (driver.Result, error) {
	if s.db.failWrite {
		return nil, errors.New("secret row details")
	}
	s.db.writes = append(s.db.writes, append([]driver.Value(nil), v...))
	return driver.RowsAffected(1), nil
}

type testRows struct {
	columns []string
	rows    [][]driver.Value
}

func result(cols []string, rows ...[]driver.Value) *testRows { return &testRows{cols, rows} }
func (r *testRows) Columns() []string                        { return r.columns }
func (r *testRows) Close() error                             { return nil }
func (r *testRows) Next(dest []driver.Value) error {
	if len(r.rows) == 0 {
		return io.EOF
	}
	copy(dest, r.rows[0])
	r.rows = r.rows[1:]
	return nil
}

func worker(t *testing.T) (*Worker, *testDB, *testDB) {
	t.Helper()
	src, dst := &testDB{source: true}, &testDB{}
	w := &Worker{source: sql.OpenDB(connector{src}), target: sql.OpenDB(connector{dst}), path: filepath.Join(t.TempDir(), "state.json"), batchSize: 500}
	w.source.SetMaxOpenConns(1)
	w.target.SetMaxOpenConns(1)
	t.Cleanup(w.Close)
	return w, src, dst
}

func TestPassPreservesRawRowsAndResumes(t *testing.T) {
	w, src, dst := worker(t)
	n, err := w.Pass(context.Background())
	if err != nil || n != 2 || dst.commits != 1 {
		t.Fatalf("pass: n=%d err=%v commits=%d", n, err, dst.commits)
	}
	if dst.writes[0][1] != nil || dst.writes[1][1] != "中文 ' secret" {
		t.Fatalf("raw values changed: %#v", dst.writes)
	}
	if _, err = w.Pass(context.Background()); err != nil || src.after != 2 || dst.commits != 1 {
		t.Fatalf("resume: %v after=%d", err, src.after)
	}
}

func TestFailuresDoNotAdvanceCheckpoint(t *testing.T) {
	for _, failure := range []string{"write", "commit"} {
		t.Run(failure, func(t *testing.T) {
			w, _, dst := worker(t)
			dst.failWrite = failure == "write"
			dst.failCommit = failure == "commit"
			if _, err := w.Pass(context.Background()); err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
			cp, err := w.load()
			if err != nil || cp.AfterID != 0 {
				t.Fatalf("advanced checkpoint: %+v %v", cp, err)
			}
			dst.failWrite = false
			dst.failCommit = false
			if _, err := w.Pass(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNoDailyReplayAndStateSaveFailure(t *testing.T) {
	w, src, dst := worker(t)
	b, _ := json.Marshal(checkpoint{AfterID: 99, CompletedAt: time.Now().Add(-25 * time.Hour)})
	if err := os.WriteFile(w.path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Pass(context.Background()); err != nil || src.after != 99 || dst.commits != 0 {
		t.Fatalf("unexpected replay: %v after=%d", err, src.after)
	}
	// Make the destination unwritable only after the source read and commit.
	oldPath := w.path
	b, _ = json.Marshal(checkpoint{})
	if err := os.WriteFile(oldPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	dst.onCommit = func() { w.path = filepath.Join(oldPath, "state.json") }
	if _, err := w.Pass(context.Background()); err == nil {
		t.Fatal("expected state error")
	}
	if dst.commits != 1 {
		t.Fatal("expected committed batch before checkpoint save failure")
	}
	w.path = oldPath
	cp, err := w.load()
	if err != nil || cp.AfterID != 0 {
		t.Fatalf("previous checkpoint changed: %+v %v", cp, err)
	}
	dst.onCommit = nil
	if _, err := w.Pass(context.Background()); err != nil || dst.commits != 2 {
		t.Fatalf("replay after state failure: %v commits=%d", err, dst.commits)
	}
}

func TestRejectSameDatabaseAndQuoteColumns(t *testing.T) {
	if w, err := Open("read@tcp(db:3306)/newapi", "write@tcp(db:3306)/newapi", "i", t.TempDir(), 500); err == nil {
		w.Close()
		t.Fatal("accepted source as destination")
	}
	q := insertSQL([]string{"id", "group", "a`b"})
	if !strings.Contains(q, "`group`") || !strings.Contains(q, "`a``b`") || strings.Contains(q, "IGNORE") {
		t.Fatal(q)
	}
}
