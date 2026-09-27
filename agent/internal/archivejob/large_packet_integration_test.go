package archivejob

import (
	"context"
	aj "controltower/internal/archivejob"
	"strings"
	"testing"
	"time"
)

func TestLargeTargetPacketLimitAndResumeMySQL(t *testing.T) {
	e, ctx := fixture(t)
	start, _ := dateBounds("2026-01-01")
	for _, x := range []struct {
		target bool
		table  string
	}{{false, "logs"}, {true, "logs_202601"}} {
		db := e.source
		if x.target {
			db = e.target
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE "+q(x.table)+" ADD content LONGTEXT"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.source.ExecContext(ctx, "INSERT INTO logs(id,created_at,type,other,content) VALUES(1,?,2,'{}',REPEAT('a',9437184))", start+1); err != nil {
		t.Fatal(err)
	}
	// This isolated test server is owned by the test invocation; restore global
	// limit before cleanup. Reconnect only target sessions to pick up the limit.
	var old int64
	if err := e.source.QueryRowContext(ctx, "SELECT @@GLOBAL.max_allowed_packet").Scan(&old); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.source.ExecContext(context.Background(), "SET GLOBAL max_allowed_packet=?", old) })
	if _, err := e.source.ExecContext(ctx, "SET GLOBAL max_allowed_packet=4194304"); err != nil {
		t.Fatal(err)
	}
	e.target.SetConnMaxLifetime(time.Nanosecond)
	if _, err := e.Step(ctx, aj.Settings{Collection: true}, 1000, 60, true); err != nil {
		t.Fatal(err)
	}
	largeSteps(t, e, ctx, aj.Settings{Collection: true}, func(s state) bool { return s.LargeCollection != nil && s.LargeCollection.Digest != "" })
	if _, err := e.Step(ctx, aj.Settings{Collection: true}, 1000, 60, true); err == nil || !strings.Contains(err.Error(), "archive_target_packet_limit") {
		t.Fatal("missing actionable limit", err)
	}
	s, err := load(ctx, e.target)
	if err != nil || s.Collection.AfterID != 0 || s.LargeCollection == nil {
		t.Fatal("packet failure advanced cursor", err)
	}
	if _, err := e.source.ExecContext(ctx, "SET GLOBAL max_allowed_packet=?", old); err != nil {
		t.Fatal(err)
	}
	largeSteps(t, e, ctx, aj.Settings{Collection: true}, func(s state) bool { return s.Collection.AfterID == 1 })
}
