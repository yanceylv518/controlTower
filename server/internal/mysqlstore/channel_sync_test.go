package mysqlstore

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/storage"
)

func TestChannelSyncPreservesAnchorsAndRejectsOldSnapshots(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN for channel synchronization integration test")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = ApplyDir(context.Background(), db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	site := fmt.Sprintf("channel-sync-%d", time.Now().UnixNano())
	ids := []string{site + "-a", site + "-b"}
	now := time.Now().UTC().Truncate(time.Second)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, id := range ids {
			for _, table := range []string{"channel_current", "channel_commands"} {
				_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", id)
			}
			_, _ = db.Exec("DELETE FROM instances WHERE id=?", id)
		}
		for _, table := range []string{"channel_base_values", "tuning_continuous_states"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site)
		}
	}()
	for _, id := range ids {
		exec(`INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, id, site, id, now, now)
		if err = s.StoreInstanceChannels(id, []channelcontrol.Channel{{ID: 7, Name: "channel", Models: "m", Weight: 10, Priority: 1, Status: 1}}, now); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE channel_base_values SET base_weight=99,base_priority=8 WHERE instance_id=?`, site)
	priority := int64(8)
	if err = s.ApplyChannelWrite(site, 7, nil, &priority, nil, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	assertValues := func(weight, priority int64) {
		t.Helper()
		rows, err := s.ListChannelBaseValues(site, "")
		if err != nil || len(rows) != 1 {
			t.Fatalf("expected one channel across collectors: %#v %v", rows, err)
		}
		if rows[0].CurrentPriority != priority || rows[0].CurrentWeight != weight || rows[0].BaseWeight != 99 || rows[0].BasePriority != 8 {
			t.Fatalf("wrong current values or overwritten anchors: %#v", rows[0])
		}
	}
	assertValues(10, 8)
	for _, id := range ids {
		if err = s.StoreInstanceChannels(id, []channelcontrol.Channel{{ID: 7, Name: "stale", Models: "different-model", Weight: 2, Priority: 1, Status: 1}}, now); err != nil {
			t.Fatal(err)
		}
		if err = s.SyncChannelSnapshotsAt(id, nil, now); err != nil {
			t.Fatal(err)
		}
	}
	assertValues(10, 8)
	// A failed Agent command cannot become the displayed current value.
	for _, status := range []string{"failed", "succeeded"} {
		command := storage.ChannelCommand{ID: site + status, InstanceID: ids[0], ChannelID: 7, CommandType: "channel.update", PayloadJSON: `{"weight":25,"priority":9}`, Status: "delivered", CreatedBy: "test", CreatedAt: now, UpdatedAt: now}
		if err = s.CreateChannelCommand(command); err != nil {
			t.Fatal(err)
		}
		if _, changed, err := s.CompleteChannelCommand(command.ID, status, "", now.Add(2*time.Second)); err != nil || !changed {
			t.Fatalf("complete: %v %v", changed, err)
		}
		if status == "failed" {
			assertValues(10, 8)
		} else {
			assertValues(25, 9)
		}
	}
	// Fresh external changes replace the confirmed result, preserving anchors.
	if err = s.StoreInstanceChannels(ids[0], []channelcontrol.Channel{{ID: 7, Name: "renamed", Models: "m", Weight: 30, Priority: 4, Status: 1}}, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	assertValues(30, 4)
}
