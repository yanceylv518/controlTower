package archivejob

import (
	aj "controltower/internal/archivejob"
	"strings"
	"testing"
)

func TestBytePagesCollectVerifySummarizeResumeMySQL(t *testing.T) {
	e, ctx := fixture(t)
	if _, err := e.source.ExecContext(ctx, "ALTER TABLE logs ADD COLUMN content LONGTEXT"); err != nil {
		t.Fatal(err)
	}
	start, _ := dateBounds("2026-01-01")
	for i := 1; i <= 4; i++ {
		at, content := start+int64(i), strings.Repeat("x", 3*1024*1024)
		if i == 4 {
			at = start + 86401
			content = "tail"
		}
		if _, err := e.source.ExecContext(ctx, "INSERT INTO logs(id,created_at,type,quota,other,content) VALUES(?,?,2,7,'{}',?)", i, at, content); err != nil {
			t.Fatal(err)
		}
	}
	// Empty old template must match the actual raw schema.
	if _, err := e.target.ExecContext(ctx, "ALTER TABLE logs_202601 ADD COLUMN content LONGTEXT"); err != nil {
		t.Fatal(err)
	}
	st, err := e.Step(ctx, aj.Settings{Collection: true}, 1000, 60, true)
	if err != nil {
		t.Fatal(err)
	}
	if st.Collection.AfterID != 2 || st.Collection.Rows != 2 {
		t.Fatalf("byte page skipped tail: %+v", st.Collection)
	}
	e.ready = false
	st, err = e.Step(ctx, aj.Settings{Collection: true}, 1000, 60, true)
	if err != nil || st.Collection.AfterID != 4 || st.Collection.Rows != 4 {
		t.Fatalf("collection resume %+v %v", st.Collection, err)
	}
	seen := map[string]bool{}
	injected := false
	for i := 0; i < 20; i++ {
		before, err := load(ctx, e.target)
		if err != nil {
			t.Fatal(err)
		}
		// Fail after summary reads, before its cursor and aggregate can commit.
		if before.History.Step == "summarize" && !injected {
			if _, err = e.target.ExecContext(ctx, "CREATE TRIGGER fail_summary BEFORE INSERT ON log_archive_daily_stats FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='test failure'"); err != nil {
				t.Fatal(err)
			}
			_, err = e.Step(ctx, aj.Settings{History: true}, 1000, 60, true)
			if err == nil {
				t.Fatal("expected injected failure")
			}
			after, err := load(ctx, e.target)
			if err != nil {
				t.Fatal(err)
			}
			if after.History != before.History {
				t.Fatal("failed summary advanced durable cursor")
			}
			if _, err = e.target.ExecContext(ctx, "DROP TRIGGER fail_summary"); err != nil {
				t.Fatal(err)
			}
			injected = true
		}
		e.ready = false
		_, err = e.Step(ctx, aj.Settings{History: true}, 1000, 60, true)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := load(ctx, e.target)
		if err != nil {
			t.Fatal(err)
		}
		if saved.History.AfterID == 2 {
			seen[saved.History.Step] = true
		}
		var status string
		if err = e.target.QueryRowContext(ctx, "SELECT state FROM log_archive_days WHERE log_date='2026-01-01'").Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "sealed" {
			break
		}
	}
	for _, phase := range []string{"verify_source", "verify_archive", "summarize"} {
		if !seen[phase] {
			t.Fatalf("short byte page incorrectly finished %s", phase)
		}
	}
	var count, quota int
	err = e.target.QueryRowContext(ctx, "SELECT SUM(CAST(JSON_UNQUOTE(amounts->'$.log_rows') AS UNSIGNED)),SUM(CAST(JSON_UNQUOTE(amounts->'$.quota') AS UNSIGNED)) FROM log_archive_daily_stats s JOIN log_archive_days d ON d.version_id=s.version_id WHERE d.log_date='2026-01-01' AND d.state='sealed'").Scan(&count, &quota)
	if err != nil || count != 3 || quota != 21 {
		t.Fatalf("missing/duplicate summary: %d %d %v", count, quota, err)
	}
}

func TestOversizedRowPreservesPrefixAndCursorMySQL(t *testing.T) {
	e, ctx := fixture(t)
	if _, err := e.source.ExecContext(ctx, "ALTER TABLE logs ADD COLUMN content LONGTEXT"); err != nil {
		t.Fatal(err)
	}
	start, _ := dateBounds("2026-01-01")
	for i, content := range []string{"small", strings.Repeat("z", maxPageBytes+1)} {
		if _, err := e.source.ExecContext(ctx, "INSERT INTO logs(id,created_at,type,other,content) VALUES(?,?,2,'{}',?)", i+1, start+int64(i+1), content); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.target.ExecContext(ctx, "ALTER TABLE logs_202601 ADD COLUMN content LONGTEXT"); err != nil {
		t.Fatal(err)
	}
	st, err := e.Step(ctx, aj.Settings{Collection: true}, 1000, 60, true)
	if err != nil || st.Collection.AfterID != 1 {
		t.Fatalf("valid prefix lost: %+v %v", st, err)
	}
	for i := 0; i < 2; i++ {
		_, err = e.Step(ctx, aj.Settings{Collection: true}, 1000, 60, true)
		if err == nil || !strings.Contains(err.Error(), "archive_row_payload_limit:id=2,bytes=") || strings.Contains(err.Error(), "zzzz") {
			t.Fatalf("unsafe/missing diagnostic: %v", err)
		}
		saved, err := load(ctx, e.target)
		if err != nil {
			t.Fatal(err)
		}
		if saved.Collection.AfterID != 1 || saved.Collection.Rows != 1 {
			t.Fatal("oversized record skipped or prefix duplicated")
		}
	}
}
