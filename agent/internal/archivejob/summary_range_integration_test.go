package archivejob

import (
	"fmt"
	"testing"
	"time"
)

func TestSummaryDateBoundaryMySQL(t *testing.T) {
	e, ctx := fixture(t)
	c, err := e.target.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = e.prepare(ctx, c); err != nil {
		t.Fatal(err)
	}
	versions := map[string]string{}
	for _, date := range []string{"2026-01-01", "2026-01-02", "2026-01-03"} {
		version := id()
		versions[date] = version
		if _, err = c.ExecContext(ctx, `INSERT INTO log_archive_days(log_date,state,version_id,updated_at) VALUES(?,'sealed',?,UTC_TIMESTAMP(6))`, date, version); err != nil {
			t.Fatal(err)
		}
		if _, err = c.ExecContext(ctx, `INSERT INTO log_archive_day_versions VALUES(?,?,1,0,'',2,UTC_TIMESTAMP(6))`, version, date); err != nil {
			t.Fatal(err)
		}
		if _, err = c.ExecContext(ctx, `INSERT INTO log_archive_live_stats(log_date,version_id,ready,updated_at) VALUES(?,?,1,UTC_TIMESTAMP(6))`, date, version); err != nil {
			t.Fatal(err)
		}
		if _, err = c.ExecContext(ctx, `INSERT INTO log_archive_daily_stats VALUES(?,'old',?,'{}','{"quota":"7"}')`, version, date); err != nil {
			t.Fatal(err)
		}
	}
	s := state{SummaryVersion: 2, FirstDate: "2026-01-01", Frontier: "2026-01-04", History: history{Date: "2026-01-01", Step: "summarize", ParserVersion: 2}, LargeHistory: &largeRecord{Token: id()}, LargeLive: &largeRecord{Token: id(), Created: 1767196801}}
	if err = ensureSummaryGeneration(ctx, c, &s, "2026-01-02", e.currentTime()); err != nil {
		t.Fatal(err)
	}
	if s.History.Date != "" || s.LargeHistory != nil || s.LargeLive != nil {
		t.Fatal("out-of-range work survived", s)
	}
	readVersion := func(date string) string {
		t.Helper()
		var v string
		if err := c.QueryRowContext(ctx, "SELECT version_id FROM log_archive_live_stats WHERE log_date=?", date).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if readVersion("2026-01-01") != versions["2026-01-01"] {
		t.Fatal("older snapshot reset")
	}
	second, third := readVersion("2026-01-02"), readVersion("2026-01-03")
	if second == versions["2026-01-02"] || third == versions["2026-01-03"] {
		t.Fatal("inclusive date not reset")
	}
	if err = e.historyStep(ctx, c, &s, 100, true); err != nil || s.History.Date != "2026-01-02" {
		t.Fatal("sealed rebuild crossed boundary", s.History, err)
	}
	// Both direct builds and incremental changes must leave the older snapshot alone.
	if err = e.buildLive(ctx, c, "2026-01-01", 100); err != nil {
		t.Fatal(err)
	}
	start, _ := dateBounds("2026-01-01")
	s.liveChanges = []rawChange{{after: testRow(map[string]string{"created_at": fmt.Sprint(start + 1), "type": "2", "quota": "999", "other": "not-json"})}}
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = flushLive(ctx, tx, &s); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var amount string
	if err = c.QueryRowContext(ctx, "SELECT amounts->>'$.quota' FROM log_archive_daily_stats WHERE version_id=?", versions["2026-01-01"]).Scan(&amount); err != nil || amount != "7" {
		t.Fatal("older amount changed", amount, err)
	}
	// Newest-first live selection may not fall through to older incomplete dates.
	if _, err = c.ExecContext(ctx, "UPDATE log_archive_live_stats SET ready=CASE WHEN log_date<'2026-01-02' THEN 0 ELSE 1 END"); err != nil {
		t.Fatal(err)
	}
	if err = e.liveStep(ctx, c, 100); err != nil {
		t.Fatal(err)
	}
	if readVersion("2026-01-01") != versions["2026-01-01"] {
		t.Fatal("live scheduler rebuilt old date")
	}
	var oldReady bool
	if err = c.QueryRowContext(ctx, "SELECT ready FROM log_archive_live_stats WHERE log_date='2026-01-01'").Scan(&oldReady); err != nil || oldReady {
		t.Fatal("live scheduler scanned excluded date", err)
	}

	if _, err = c.ExecContext(ctx, "UPDATE log_archive_days SET state='pending' WHERE log_date IN ('2026-01-01','2026-01-03')"); err != nil {
		t.Fatal(err)
	}
	s.History = history{}
	if err = e.historyStep(ctx, c, &s, 100, true); err != nil || s.History.Date != "2026-01-03" {
		t.Fatal("pending selection crossed boundary", s.History, err)
	}
	// Moving later freezes excluded days; moving earlier resets only newly included days.
	if err = ensureSummaryGeneration(ctx, c, &s, "2026-01-03", e.currentTime()); err != nil {
		t.Fatal(err)
	}
	if readVersion("2026-01-02") != second || readVersion("2026-01-03") != third {
		t.Fatal("narrowing reset existing data")
	}
	if err = ensureSummaryGeneration(ctx, c, &s, "2026-01-01", e.currentTime()); err != nil {
		t.Fatal(err)
	}
	if readVersion("2026-01-01") == versions["2026-01-01"] || readVersion("2026-01-02") == second || readVersion("2026-01-03") != third {
		t.Fatal("expansion reset wrong range")
	}
}

func TestSummaryDefaultDatePersistsMySQL(t *testing.T) {
	e, ctx := fixture(t)
	c, err := e.target.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = e.prepare(ctx, c); err != nil {
		t.Fatal(err)
	}
	s := state{SummaryVersion: 2}
	now := time.Date(2026, 1, 1, 16, 30, 0, 0, time.UTC)
	if err = ensureSummaryGeneration(ctx, c, &s, "", now); err != nil {
		t.Fatal(err)
	}
	if s.SummaryFromDate != "2026-01-02" {
		t.Fatal("default not Beijing date", s.SummaryFromDate)
	}
	restarted, err := load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if err = ensureSummaryGeneration(ctx, c, &restarted, "", now.AddDate(0, 0, 3)); err != nil {
		t.Fatal(err)
	}
	if restarted.SummaryFromDate != "2026-01-02" {
		t.Fatal("default moved on restart")
	}
}
