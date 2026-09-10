package mysqlstore

import (
	"context"
	ac "controltower/internal/archivecontrol"
	"controltower/server/internal/storage"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestLogArchiveControlLifecycle(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires test MySQL")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	id := fmt.Sprintf("archive-test-%d", time.Now().UnixNano())
	if err = s.CreateInstance(storage.Instance{ID: id, Enabled: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"site_log_archive_days", "log_archive_targets", "site_log_archive_control", "log_archive_control", "operation_audits", "instances"} {
			key := "instance_id"
			if table == "site_log_archive_control" || table == "site_log_archive_days" {
				key = "site_id"
			}
			if table == "instances" {
				key = "id"
			}
			_, _ = db.Exec("DELETE FROM "+table+" WHERE "+key+"=?", id)
		}
	}()
	st := ac.Status{AgentID: "agent", Session: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Configured: true, State: "paused"}
	member := id + "-b"
	if err = s.CreateInstance(storage.Instance{ID: member, SiteID: id, Enabled: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Exec("DELETE FROM log_archive_targets WHERE instance_id=?", member)
		_, _ = db.Exec("DELETE FROM instances WHERE id=?", member)
	}()
	second := st
	second.AgentID = "second-agent"
	second.Session = "cccccccccccccccccccccccccccccccc"
	if _, err = s.PollLogArchive(ctx, member, second); err != nil {
		t.Fatal(err)
	}
	out, err := s.PollLogArchive(ctx, id, st)
	if err != nil || out.Granted {
		t.Fatal(out, err)
	}
	c := out.Config
	c.AgentID = "agent"
	c.InstanceID = id
	c.Running = true
	if err = s.UpdateLogArchive(ctx, id, c, "tester"); err != nil {
		t.Fatal(err)
	}
	if v, e := s.PollLogArchive(ctx, member, second); e != nil || v.Granted || v.SiteID != id {
		t.Fatal("same-site second executor granted", v, e)
	}
	if list, e := s.ListLogArchives(ctx, id); e != nil || len(list) != 1 || len(list[0].Targets) != 2 {
		t.Fatal("site aggregation failed", list, e)
	}
	if err = s.UpdateLogArchive(ctx, "other-site", c, "tester"); !errors.Is(err, ac.ErrConflict) {
		t.Fatal("cross-site executor accepted", err)
	}
	if err = s.UpdateLogArchive(ctx, id, c, "tester"); !errors.Is(err, ac.ErrConflict) {
		t.Fatal("stale config accepted", err)
	}
	out, err = s.PollLogArchive(ctx, id, st)
	if err != nil || !out.Granted {
		t.Fatal(out, err)
	}
	other := st
	st.Days = []ac.Day{{Date: "2026-09-08", ArchivedRows: "500", RequestRows: "490", ErrorRows: "10", LastID: 500, VerifiedAt: time.Now().UTC().Truncate(time.Microsecond)}}
	for i := 0; i < 2; i++ {
		if _, err = s.PollLogArchive(ctx, id, st); err != nil {
			t.Fatal(err)
		}
	}
	days, err := s.ListLogArchiveDays(ctx, id, "2026-09")
	if err != nil || len(days) != 1 || days[0].ArchivedRows != "500" {
		t.Fatalf("repeated day receipt: %+v %v", days, err)
	}
	if days, err = s.ListLogArchiveDays(ctx, "other-site", "2026-09"); err != nil || len(days) != 0 {
		t.Fatal("cross-site daily records", days, err)
	}
	if days, err = s.ListLogArchiveDays(ctx, id, "2026-08"); err != nil || len(days) != 0 {
		t.Fatal("wrong month daily records", days, err)
	}
	other.Session = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if v, e := s.PollLogArchive(ctx, id, other); e != nil || v.Granted {
		t.Fatal("duplicate process grant", v, e)
	}
	c = out.Config
	c.Running = false
	if err = s.UpdateLogArchive(ctx, id, c, "tester"); err != nil {
		t.Fatal(err)
	}
	if v, e := s.PollLogArchive(ctx, id, st); e != nil || v.Granted || v.Config.Running {
		t.Fatal("pause not delivered", v, e)
	}
}
