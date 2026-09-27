package archivejob

import (
	"context"
	aj "controltower/internal/archivejob"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
)

type pageConnector struct{ values [][]driver.Value }

func (p pageConnector) Connect(context.Context) (driver.Conn, error) { return pageConn{p}, nil }
func (p pageConnector) Driver() driver.Driver                        { return pageDriver{} }

type pageDriver struct{}

func (pageDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unused") }

type pageConn struct{ pageConnector }

func (pageConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (pageConn) Close() error                        { return nil }
func (pageConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (p pageConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &pageRows{values: p.values}, nil
}

type pageRows struct {
	values [][]driver.Value
	index  int
}

func (*pageRows) Columns() []string { return []string{"id", "content"} }
func (*pageRows) Close() error      { return nil }
func (p *pageRows) Next(dest []driver.Value) error {
	if p.index == len(p.values) {
		return io.EOF
	}
	copy(dest, p.values[p.index])
	p.index++
	return nil
}
func pageDB(t *testing.T, values ...[]driver.Value) *sql.DB {
	t.Helper()
	db := sql.OpenDB(pageConnector{values})
	t.Cleanup(func() { db.Close() })
	return db
}

func TestLargeRecordBytePages(t *testing.T) {
	ctx := context.Background()
	large := strings.Repeat("L", 11613906)
	cases := []struct {
		name    string
		values  [][]driver.Value
		want    int
		limited bool
	}{
		{"prefix", [][]driver.Value{{"1", "small"}, {"2", large}, {"3", "tail"}}, 1, true},
		{"singleton before tail", [][]driver.Value{{"2", large}, {"3", "tail"}}, 1, true},
		{"singleton EOF", [][]driver.Value{{"2", large}}, 1, false},
		{"empty", nil, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, limited, err := readPage(ctx, pageDB(t, tc.values...), "select")
			if err != nil || len(rows) != tc.want || limited != tc.limited {
				t.Fatalf("rows=%d limited=%v err=%v", len(rows), limited, err)
			}
			if len(rows) > 0 && rows[0].text("id") == "2" && rows[0].text("content") != large {
				t.Fatal("record truncated")
			}
		})
	}
	rows, err := readRows(ctx, pageDB(t, []driver.Value{"2", large}), "point lookup")
	if err != nil || len(rows) != 1 || rows[0].text("content") != large {
		t.Fatal("large target comparison failed", err)
	}
	tooLarge := strings.Repeat("x", maxRowBytes+1)
	rows, limited, err := readPage(ctx, pageDB(t, []driver.Value{"1", "small"}, []driver.Value{"2", tooLarge}), "select")
	if err != nil || !limited || len(rows) != 1 {
		t.Fatal("valid prefix lost", err)
	}
	_, _, err = readPage(ctx, pageDB(t, []driver.Value{"2", tooLarge}), "select")
	if err == nil || !strings.Contains(err.Error(), "archive_row_payload_limit:id=2,") || strings.Contains(err.Error(), "xxxx") {
		t.Fatal("unsafe or missing error", err)
	}
}

func TestFailedTaskDoesNotStarveOtherTask(t *testing.T) {
	for _, historyFails := range []bool{false, true} {
		s := state{}
		s.Collection.AfterID = 42
		s.History.AfterID = 99
		settings := aj.Settings{Collection: true, History: true, CollectionBatches: 1, HistoryBatches: 4}
		got := ""
		for i := 0; i < 10; i++ {
			attempt := s
			h := attempt.nextHistoryTurn(settings)
			if h {
				got += "H"
			} else {
				got += "C"
			}
			if h == historyFails {
				attempt.Collection.AfterID = 1000
				attempt.History.AfterID = 2000
				attempt.HistoryProgress.Rows = 999
				attempt.Collection.Error = "failure"
				attempt.HistoryProgress.Error = "failure"
				s = failedAttempt(s, attempt, h)
			} else {
				s = attempt
			}
			if s.Collection.AfterID != 42 || s.History.AfterID != 99 || s.HistoryProgress.Rows != 0 {
				t.Fatal("failed data progress committed")
			}
		}
		if got != "CHHHHCHHHH" {
			t.Fatalf("task starved: %s", got)
		}
	}
}
