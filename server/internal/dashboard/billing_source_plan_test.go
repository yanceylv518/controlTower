package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"controltower/server/internal/mysqlstore"
	"database/sql"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

// Opt-in: EXPLAIN only, never EXPLAIN ANALYZE or source schema changes.
func TestBillingSourceReadPlan(t *testing.T) {
	site := os.Getenv("CT_BILLING_PLAN_SITE")
	if site == "" {
		t.Skip("requires explicitly selected local readonly site")
	}
	db, err := mysqlstore.Open(os.Getenv("CT_DATABASE_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := &PassthroughHandler{Config: mysqlstore.New(db), SecretKey: os.Getenv("CT_SECRET_KEY")}
	source, configured, err := h.database(site)
	if err != nil || !configured {
		t.Fatal("source unavailable", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	q, _ := billingLogsPageQuery(5, 0, -1)
	day := time.Date(2026, 6, 2, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))
	old := billingLogColumns + billingOtherProjection + ` FROM logs l LEFT JOIN channels c ON c.id=l.channel_id WHERE l.type=2 AND l.created_at>=? AND l.created_at<? AND (l.created_at>? OR (l.created_at=? AND l.id>?)) AND l.user_id=? ORDER BY l.created_at,l.id LIMIT ?`
	read := func(query string, args []any) []billing.PagedLogRecord {
		rows, e := source.QueryContext(ctx, query, args...)
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		v, e := scanBillingLogRows(rows, nil)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	// Initial and resumed pages: four bounded 17-row reads, only when enabled.
	cursor := billing.LogCursor{}
	for _, phase := range []string{"initial", "resumed"} {
		oldArgs := []any{day.Unix(), day.AddDate(0, 0, 1).Unix(), cursor.CreatedUnix, cursor.CreatedUnix, cursor.ID, 5, 17}
		newArgs := append(billingPageRangeArgs(day, day.AddDate(0, 0, 1), cursor), 5, 17)
		explainBillingPlan(t, ctx, source, phase+" before", old, oldArgs...)
		explainBillingPlan(t, ctx, source, phase+" after", q, newArgs...)
		before, after := read(old, oldArgs), read(q, newArgs)
		if !reflect.DeepEqual(before, after) {
			t.Fatal(phase, "bounded sample changed")
		}
		t.Logf("%s before/after %d-row normalized samples match", phase, len(after))
		if len(after) == 0 {
			break
		}
		last := after[len(after)-1]
		cursor = billing.LogCursor{CreatedUnix: last.CreatedUnix, ID: last.ID}
	}
}
func explainBillingPlan(t *testing.T, ctx context.Context, db *sql.DB, label, q string, args ...any) {
	t.Helper()
	var raw string
	if err := db.QueryRowContext(ctx, "EXPLAIN FORMAT=JSON "+q, args...).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["table_name"] != nil {
				t.Logf("%s table=%v access=%v key=%v estimated_rows=%v", label, x["table_name"], x["access_type"], x["key"], x["rows_examined_per_scan"])
			}
			if x["using_filesort"] != nil {
				t.Logf("%s filesort=%v", label, x["using_filesort"])
			}
			for _, child := range x {
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(v)
}
