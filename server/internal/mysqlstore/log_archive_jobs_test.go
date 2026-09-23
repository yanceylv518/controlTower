package mysqlstore

import (
	"context"
	ac "controltower/internal/archivecontrol"
	aj "controltower/internal/archivejob"
	"controltower/server/internal/storage"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestArchiveJobsCoexistsWithLegacyControl(t *testing.T) {
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
	id := fmt.Sprintf("jobs-test-%d", time.Now().UnixNano())
	store := New(db)
	if err = store.CreateInstance(storage.Instance{ID: id, Enabled: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, q := range []string{"DELETE FROM log_archive_day_reports WHERE site_id=?", "DELETE FROM log_archive_job_control WHERE site_id=?", "DELETE FROM log_archive_executors WHERE instance_id=?", "DELETE FROM log_archive_control WHERE instance_id=?", "DELETE FROM instances WHERE id=?"} {
			_, _ = db.Exec(q, id)
		}
	}()
	// The full migration chain has the legacy instance-keyed table before 091.
	if _, err = db.Exec("INSERT INTO log_archive_control(instance_id,config_json,status_json) VALUES(?,'{}','{}')", id); err != nil {
		t.Fatal(err)
	}
	jobs := archiveJobsStore{store}
	if _, err = jobs.ListLogArchives(ctx, id); err != nil {
		t.Fatal(err)
	}
	st := ac.Status{AgentID: "agent", Session: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Configured: true, State: "paused", Engine: &aj.Status{Protocol: 1}}
	res, err := jobs.PollLogArchive(ctx, id, st)
	if err != nil {
		t.Fatal(err)
	}
	if !res.StatusAccepted || res.Granted {
		t.Fatalf("unexpected grant: %+v", res)
	}
	items, err := jobs.ListLogArchives(ctx, id)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if items[0].Config.Tasks == nil || items[0].Config.AgentID != "agent" {
		t.Fatal("new control not persisted")
	}
	var raw string
	if err = db.QueryRow("SELECT config_json FROM log_archive_control WHERE instance_id=?", id).Scan(&raw); err != nil || raw != "{}" {
		t.Fatalf("legacy control changed: %s %v", raw, err)
	}
}
