package archivejob

import (
	"context"
	aj "controltower/internal/archivejob"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func largeSteps(t *testing.T, e *Engine, ctx context.Context, settings aj.Settings, done func(state) bool) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		s, err := load(ctx, e.target)
		if err != nil {
			t.Fatal(err)
		}
		if done(s) {
			return
		}
		step, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err = e.Step(step, settings, 1000, 60, true)
		cancel()
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if i%40 == 0 {
			t.Logf("large step %d", i)
		}
		e.ready = false
	}
	t.Fatal("large transfer did not finish")
}
func TestLargeRecord181MiBEndToEndMySQL(t *testing.T) {
	e, ctx := fixtureTimeout(t, 15*time.Minute)
	start, _ := dateBounds("2026-01-01")
	for _, db := range []*sql.DB{e.source, e.target} {
		name := "logs"
		if db == e.target {
			name = "logs_202601"
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE "+q(name)+" MODIFY other LONGTEXT, ADD content LONGBLOB"); err != nil {
			t.Fatal(err)
		}
	}
	// Use the server to construct a large fixture; no 181 MiB argument/result is
	// materialized in the Go test or the Agent.
	const size = 189629491
	if _, err := e.source.ExecContext(ctx, "INSERT INTO logs(id,created_at,type,quota,other,content) VALUES(1,?,2,7,CONCAT('{\"ignored\":\"',REPEAT('x',?), '\",\"cache_tokens\":17,\"model_ratio\":0.5}'),UNHEX('00FF61')), (2,?,2,9,'{}',NULL)", start+1, size, start+86401); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Step(ctx, aj.Settings{Collection: true}, 1000, 60, true); err != nil {
		t.Fatal(err)
	}
	s, err := load(ctx, e.target)
	if err != nil || s.LargeCollection == nil || s.Collection.AfterID != 0 {
		t.Fatal("preflight did not stage", err)
	}
	// Chunk write failure must roll back progress and retain the pending ID.
	if _, err = e.target.ExecContext(ctx, "CREATE TRIGGER fail_chunk BEFORE INSERT ON log_archive_large_chunks FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='test failure'"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Step(ctx, aj.Settings{Collection: true}, 1000, 60, true); err == nil {
		t.Fatal("expected chunk failure")
	}
	after, err := load(ctx, e.target)
	if err != nil || after.LargeCollection.Column != 0 || after.Collection.AfterID != 0 {
		t.Fatal("failed chunk advanced", err)
	}
	if _, err = e.target.ExecContext(ctx, "DROP TRIGGER fail_chunk"); err != nil {
		t.Fatal(err)
	}
	largeSteps(t, e, ctx, aj.Settings{Collection: true}, func(s state) bool { return s.LargeCollection != nil && s.LargeCollection.Digest != "" })
	var count int
	if err = e.target.QueryRowContext(ctx, "SELECT COUNT(*) FROM logs_202601 WHERE id=1").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial row leaked", count, err)
	}
	// Corrupt the final write; byte verification must roll back both raw row and cursor.
	if _, err = e.target.ExecContext(ctx, "CREATE TRIGGER corrupt_large BEFORE INSERT ON logs_202601 FOR EACH ROW SET NEW.other='{}'"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Step(ctx, aj.Settings{Collection: true}, 1000, 60, true); err == nil || !strings.Contains(err.Error(), "archive_large_publish_mismatch") {
		t.Fatal("publish corruption accepted", err)
	}
	if err = e.target.QueryRowContext(ctx, "SELECT COUNT(*) FROM logs_202601 WHERE id=1").Scan(&count); err != nil || count != 0 {
		t.Fatal("rollback lost", err)
	}
	if _, err = e.target.ExecContext(ctx, "DROP TRIGGER corrupt_large"); err != nil {
		t.Fatal(err)
	}
	largeSteps(t, e, ctx, aj.Settings{Collection: true}, func(s state) bool { return s.Collection.AfterID == 2 })
	largeSteps(t, e, ctx, aj.Settings{History: true}, func(s state) bool {
		var state string
		e.target.QueryRowContext(ctx, "SELECT state FROM log_archive_days WHERE log_date='2026-01-01'").Scan(&state)
		return state == "sealed"
	})
	var bytes int64
	var binary string
	if err = e.target.QueryRowContext(ctx, "SELECT OCTET_LENGTH(other),HEX(content) FROM logs_202601 WHERE id=1").Scan(&bytes, &binary); err != nil || bytes < size || binary != "00FF61" {
		t.Fatal("raw content lost", bytes, binary, err)
	}
	var quota, cache string
	if err = e.target.QueryRowContext(ctx, "SELECT amounts->>'$.quota',amounts->>'$.cache_tokens' FROM log_archive_daily_stats s JOIN log_archive_days d ON d.version_id=s.version_id WHERE d.log_date='2026-01-01'").Scan(&quota, &cache); err != nil || quota != "7" || cache != "17" {
		t.Fatal("wrong summary", quota, cache, err)
	}
	largeSteps(t, e, ctx, aj.Settings{Collection: true}, func(s state) bool {
		var ready bool
		e.target.QueryRowContext(ctx, "SELECT ready FROM log_archive_live_stats WHERE log_date='2026-01-01'").Scan(&ready)
		return ready
	})
	if err = e.target.QueryRowContext(ctx, "SELECT amounts->>'$.quota',amounts->>'$.cache_tokens' FROM log_archive_daily_stats s JOIN log_archive_live_stats d ON d.version_id=s.version_id WHERE d.log_date='2026-01-01'").Scan(&quota, &cache); err != nil || quota != "7" || cache != "17" {
		t.Fatal("wrong live summary", quota, cache, err)
	}
	if err = e.target.QueryRowContext(ctx, "SELECT COUNT(*) FROM log_archive_large_chunks").Scan(&count); err != nil || count != 0 {
		t.Fatal("staging chunks leaked", count, err)
	}
}
