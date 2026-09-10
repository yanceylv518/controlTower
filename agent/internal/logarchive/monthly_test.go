package logarchive

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

func monthlyRow(id int64, date string, typ string, tokens string) []any {
	d, _ := time.Parse(time.RFC3339, date)
	return []any{strconv.FormatInt(id, 10), "body", strconv.FormatInt(d.Unix(), 10), typ, tokens, "20", "30"}
}

func TestMonthlyBoundaryAndUndated(t *testing.T) {
	for _, tc := range []struct{ date, day, month string }{
		{"2026-08-31T15:59:59Z", "2026-08-31", "202608"},
		{"2026-08-31T16:00:00Z", "2026-09-01", "202609"},
		{"2026-12-31T16:00:00Z", "2027-01-01", "202701"},
	} {
		_, c, err := rowContribution(testColumns, monthlyRow(1, tc.date, "2", "10"))
		if err != nil || c.Day != tc.day || c.month() != tc.month {
			t.Fatalf("%s: %+v %v", tc.date, c, err)
		}
	}
	r := monthlyRow(1, "2026-01-01T00:00:00Z", "1", "999")
	r[2] = nil
	_, c, err := rowContribution(testColumns, r)
	if err != nil || c.Day != "undated" || c.Values[1] != "0" || c.Values[4] != "0" {
		t.Fatalf("undated/non-request: %+v %v", c, err)
	}
}

func TestMonthlyReplayCorrectionAndRollback(t *testing.T) {
	w, _, dst := worker(t)
	ctx := context.Background()
	row := monthlyRow(10, "2026-08-31T15:59:59Z", "2", "9007199254740993")
	write := func() {
		t.Helper()
		if err := writeMonthlyBatch(ctx, w.target, testColumns, [][]any{row}); err != nil {
			t.Fatal(err)
		}
	}
	write()
	write()
	if v := dst.stats["log_daily_stats:2026-08-31"]; v[0] != "1" || v[4] != "9007199254740993" {
		t.Fatalf("replay or precision: %v", v)
	}
	row = monthlyRow(10, "2026-08-31T16:00:00Z", "5", "100")
	dst.failStats = true
	if err := writeMonthlyBatch(ctx, w.target, testColumns, [][]any{row}); err == nil {
		t.Fatal("expected stats failure")
	}
	if dst.stats["log_daily_stats:2026-08-31"][0] != "1" || strings.Contains(dst.ledger[10], "2026-09-01") {
		t.Fatal("stats failure committed partial state")
	}
	dst.failStats = false
	write()
	if dst.stats["log_monthly_stats:202608"][0] != "0" || dst.stats["log_monthly_stats:202609"][0] != "1" || dst.stats["log_daily_stats:2026-09-01"][3] != "1" {
		t.Fatalf("correction not transferred: %#v", dst.stats)
	}
	found := false
	for _, q := range dst.queries {
		if q == "DELETE FROM `logs_202608` WHERE id=?" {
			found = true
		}
	}
	if !found {
		t.Fatal("old month detail was not relocated")
	}
}

func TestMonthlyBatchSpansMonths(t *testing.T) {
	w, _, dst := worker(t)
	rows := [][]any{monthlyRow(1, "2026-08-01T00:00:00Z", "2", "10"), monthlyRow(2, "2026-09-01T00:00:00Z", "5", "20")}
	if err := writeMonthlyBatch(context.Background(), w.target, testColumns, rows); err != nil {
		t.Fatal(err)
	}
	if dst.commits != 1 || len(dst.stats) != 4 {
		t.Fatalf("batch not atomic across months: %d %#v", dst.commits, dst.stats)
	}
	for _, month := range []string{"202608", "202609"} {
		found := false
		for _, q := range dst.queries {
			if strings.HasPrefix(q, "INSERT INTO `logs_"+month+"`") {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing table %s", month)
		}
	}
}
