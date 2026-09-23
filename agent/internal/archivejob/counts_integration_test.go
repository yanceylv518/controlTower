package archivejob

import (
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"testing"

	aj "controltower/internal/archivejob"
)

func insertCountLog(t *testing.T, e *Engine, ctx context.Context, id, at int64) {
	t.Helper()
	if _, err := e.target.ExecContext(ctx, "INSERT INTO "+q(table(day(at)))+"(id,created_at) VALUES(?,?)", id, at); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshCountsPausedArchiveWithoutSourceMySQL(t *testing.T) {
	e, ctx := fixture(t)
	start, _ := dateBounds("2026-01-01")
	insertCountLog(t, e, ctx, 1, start)
	insertCountLog(t, e, ctx, 3, start+86399)
	insertCountLog(t, e, ctx, 2, start+2*86400)
	// A paused refresh of existing archives must not need the source connection.
	e.source.Close()
	st, err := e.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Valid() || st.Collection.AfterID != 3 || st.Collection.Rows != 0 || st.History.Step != "" {
		t.Fatalf("reporting advanced a task: %+v", st)
	}
	want := map[string]string{"2026-01-01": "2", "2026-01-02": "0", "2026-01-03": "1"}
	for _, d := range st.Days {
		if want[d.Date] != d.Rows || d.State == "sealed" {
			t.Fatalf("count or seal changed: %+v", d)
		}
		delete(want, d.Date)
	}
	if len(want) != 0 {
		t.Fatal("missing daily counts", want)
	}
	before, err := load(ctx, e.target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := load(ctx, e.target)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("refresh moved task cursors", before, after, err)
	}
}

func TestPagedCountNeverPublishesPartialOrStaleTotalsMySQL(t *testing.T) {
	e, ctx := fixture(t)
	date := "2026-01-02"
	start, end := dateBounds(date)
	// Repeated timestamps, unordered IDs, large IDs, and both day boundaries.
	for _, r := range [][2]int64{{99, start - 1}, {90, start}, {1, start}, {9007199254740993, start}, {2, start + 1}, {3, end - 1}, {100, end}} {
		insertCountLog(t, e, ctx, r[0], r[1])
	}
	// This index sorts type before ID and must not be used for the paged scan.
	if _, err := e.target.ExecContext(ctx, "ALTER TABLE logs_202601 ADD INDEX aaa_wrong_order(created_at,type)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Step(ctx, aj.Settings{}, 10, 60, false); err != nil {
		t.Fatal(err)
	}
	c, err := e.target.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var revision uint64
	if err := c.QueryRowContext(ctx, "SELECT revision FROM log_archive_days WHERE log_date=?", date).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	e.counts = map[string]*dayCount{date: {revision: revision, started: true, created: start, id: -1}}
	for page := 0; page < 3; page++ {
		done, err := e.countDay(ctx, c, date, revision, 2)
		if err != nil || done != (page == 2) {
			t.Fatalf("page %d: complete=%t err=%v", page, done, err)
		}
		var rows sql.NullInt64
		if err := c.QueryRowContext(ctx, "SELECT raw_rows FROM log_archive_days WHERE log_date=?", date).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows.Valid != done || (done && rows.Int64 != 5) {
			t.Fatalf("partial/wrong count published: %+v", rows)
		}
	}
	// Restart a scan, then mutate an earlier portion of the day using the same
	// revision invalidation as raw archive writes.
	if _, err := c.ExecContext(ctx, "UPDATE log_archive_days SET raw_rows=NULL WHERE log_date=?", date); err != nil {
		t.Fatal(err)
	}
	e.counts[date] = &dayCount{revision: revision, started: true, created: start, id: -1}
	if done, err := e.countDay(ctx, c, date, revision, 2); err != nil || done {
		t.Fatal("first page failed", done, err)
	}
	s, err := load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	idText, atText := "0", strconv.FormatInt(start, 10)
	if _, _, err := writeRaw(ctx, tx, row{"id": &idText, "created_at": &atText}, &s); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// A stale revision cannot publish, even if it reaches the end of its scan.
	if done, err := e.countDay(ctx, c, date, revision, 10); err != nil || !done {
		t.Fatal("stale scan did not finish", done, err)
	}
	var rows sql.NullInt64
	if err := c.QueryRowContext(ctx, "SELECT raw_rows,revision FROM log_archive_days WHERE log_date=?", date).Scan(&rows, &revision); err != nil || rows.Valid {
		t.Fatal("stale total published", rows, err)
	}
	// A partial cursor with a previous revision must restart at the day boundary.
	e.counts[date] = &dayCount{revision: revision - 1, started: true, created: end - 1, id: 3, rows: 5}
	if done, err := e.countDay(ctx, c, date, revision, 2); err != nil || !done {
		t.Fatal("fresh count failed", done, err)
	}
	if err := c.QueryRowContext(ctx, "SELECT raw_rows FROM log_archive_days WHERE log_date=?", date).Scan(&rows); err != nil || !rows.Valid || rows.Int64 != 6 {
		t.Fatal("revision restart lost a row", rows, err)
	}
}

func TestCountFailureReportedWithoutStarvingOtherMonthsMySQL(t *testing.T) {
	e, ctx := fixture(t)
	start, _ := dateBounds("2026-01-31")
	insertCountLog(t, e, ctx, 1, start)
	if _, err := e.target.ExecContext(ctx, "CREATE TABLE logs_202602 LIKE logs_202601"); err != nil {
		t.Fatal(err)
	}
	insertCountLog(t, e, ctx, 2, start+86400)
	if _, err := e.Step(ctx, aj.Settings{}, 10, 60, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.target.ExecContext(ctx, "ALTER TABLE logs_202601 DROP INDEX created_at"); err != nil {
		t.Fatal(err)
	}
	st, err := e.Refresh(ctx)
	if err == nil || st.CountsError != "archive_count_created_at_index_required" || st.CountsDate != "2026-01-31" || !st.Valid() {
		t.Fatal("failed count was hidden", st, err)
	}
	// The next refresh can count February even though January still fails.
	_, _ = e.Refresh(ctx)
	var a, b sql.NullInt64
	if err := e.target.QueryRowContext(ctx, "SELECT raw_rows FROM log_archive_days WHERE log_date='2026-01-31'").Scan(&a); err != nil {
		t.Fatal(err)
	}
	if err := e.target.QueryRowContext(ctx, "SELECT raw_rows FROM log_archive_days WHERE log_date='2026-02-01'").Scan(&b); err != nil || a.Valid || !b.Valid || b.Int64 != 1 {
		t.Fatal("failed day blocked valid day or became zero", a, b, err)
	}
}

func TestStepDoesNotSkipDailyReportPagesMySQL(t *testing.T) {
	e, ctx := fixture(t)
	start, _ := dateBounds("2026-01-01")
	insertCountLog(t, e, ctx, 1, start)
	if _, err := e.target.ExecContext(ctx, "CREATE TABLE logs_202605 LIKE logs_202601"); err != nil {
		t.Fatal(err)
	}
	end, _ := dateBounds("2026-05-01")
	insertCountLog(t, e, ctx, 2, end)
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		if _, err := e.Step(ctx, aj.Settings{}, 10, 60, false); err != nil {
			t.Fatal(err)
		}
		st, err := e.Refresh(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range st.Days {
			if d.Rows != "" {
				seen[d.Date] = true
			}
		}
	}
	if len(seen) != 121 {
		t.Fatalf("report pages skipped known counts: got %d days, want 121", len(seen))
	}
}
