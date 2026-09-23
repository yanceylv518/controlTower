package logarchive

import "testing"

func TestCalendarOriginMySQL(t *testing.T) {
	w, ctx, _, _ := scanFixture(t)
	if _, err := w.source.ExecContext(ctx, `INSERT INTO logs(id,created_at) VALUES(1,1767225599)`); err != nil {
		t.Fatal(err)
	}
	o, err := w.CalendarOrigin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.Source != "source" || o.Date != "2026-01-01" {
		t.Fatalf("source fallback/Beijing boundary: %+v", o)
	}
	for _, query := range []string{
		`CREATE TABLE logs_202602(id BIGINT PRIMARY KEY,created_at BIGINT, INDEX(created_at))`,
		`CREATE TABLE logs_202603(id BIGINT PRIMARY KEY,created_at BIGINT, INDEX(created_at))`,
		`INSERT INTO logs_202602 VALUES(1,1770000000),(99,1769904000)`,
	} {
		if _, err := w.target.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	// Archive takes precedence even when the source has older records. Minimum
	// timestamp need not belong to the minimum ID; empty newer months are ignored.
	o, err = w.CalendarOrigin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.Source != "archive" || o.Date != "2026-02-01" {
		t.Fatalf("archive origin: %+v", o)
	}
	if _, err := w.target.ExecContext(ctx, `ALTER TABLE logs_202602 DROP INDEX created_at`); err != nil {
		t.Fatal(err)
	}
	if _, err = w.CalendarOrigin(ctx); err == nil {
		t.Fatal("must not fallback to source when archive query fails")
	}
}
