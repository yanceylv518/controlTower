package mysqlstore

import (
	"context"
	"controltower/internal/channelcontrol"
	"controltower/server/internal/storage"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestChannelSourcePreservesSiteAnchors(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated CT_MYSQL_TEST_DSN")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = ApplyDir(context.Background(), db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	for _, nonempty := range []bool{false, true} {
		t.Run(fmt.Sprint(nonempty), func(t *testing.T) {
			s := New(db)
			site := fmt.Sprintf("source-%d", time.Now().UnixNano())
			a, b := site+"a", site+"b"
			now := time.Now().UTC().Add(-time.Minute)
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := db.Exec(q, args...); err != nil {
					t.Fatal(err)
				}
			}
			defer func() {
				for _, table := range []string{"channel_current", "channel_base_values", "tuning_continuous_states"} {
					db.Exec("DELETE FROM "+table+" WHERE instance_id IN (?,?,?)", site, a, b)
				}
				db.Exec("DELETE FROM instances WHERE id IN (?,?)", a, b)
			}()
			for _, id := range []string{a, b} {
				exec(`INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, id, site, id, now, now)
			}
			channels := []channelcontrol.Channel{{ID: 7, Name: "c", Models: "m", Weight: 100, Priority: 11, Status: 1}}
			for _, id := range []string{a, b} {
				if err := s.StoreInstanceChannels(id, channels, now); err != nil {
					t.Fatal(err)
				}
			}
			exec(`UPDATE channel_base_values SET base_priority=12 WHERE instance_id=?`, site)
			exec(`INSERT INTO tuning_continuous_states(instance_id,channel_id,model_name,phase,updated_at) VALUES(?,7,'m','circuit',?)`, site, now)
			assert := func(priority int64) {
				t.Helper()
				var base, online int64
				var phase string
				if err := db.QueryRow(`SELECT base_priority FROM channel_base_values WHERE instance_id=? AND channel_id=7`, site).Scan(&base); err != nil || base != 12 {
					t.Fatalf("base lost: %d %v", base, err)
				}
				if err := db.QueryRow(`SELECT phase FROM tuning_continuous_states WHERE instance_id=? AND channel_id=7`, site).Scan(&phase); err != nil || phase != "circuit" {
					t.Fatalf("state lost: %s %v", phase, err)
				}
				rows, err := s.ListChannelBaseValues(site, "")
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range rows {
					if r.ChannelID == 7 {
						online = r.CurrentPriority
					}
				}
				if online != priority {
					t.Fatalf("online=%d want=%d", online, priority)
				}
			}
			var replacement []channelcontrol.Channel
			if nonempty {
				replacement = []channelcontrol.Channel{{ID: 8, Name: "other", Models: "m", Weight: 1, Priority: 1, Status: 1}}
			}
			if err := s.StoreInstanceChannels(a, replacement, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			assert(11)
			channels[0].Priority = 0
			if err := s.StoreInstanceChannels(b, channels, now.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			assert(0)
			if err := s.UpdateReadonlyDSNForSite(site, "configured", now); err != nil {
				t.Fatal(err)
			}
			if ok, err := s.AcceptAgentChannelSnapshots(a); err != nil || ok {
				t.Fatal("Agent inventory still accepted")
			}
			if err := s.SyncChannelSnapshotsAt(b, nil, now.Add(3*time.Second)); err != nil {
				t.Fatal(err)
			}
			assert(0)
			p := int64(99)
			if err := s.InsertChannelSnapshot(storage.ChannelSnapshot{InstanceID: b, ChannelID: 7, ID: site, Status: "enabled", ModelsText: "m", Priority: &p, CapturedAt: now.Add(4 * time.Second)}); err != nil {
				t.Fatal(err)
			}
			assert(0)
			if err := s.StoreReadonlyChannels(site, channels, now.Add(5*time.Second), "old-config"); err == nil {
				t.Fatal("stale source config applied")
			}
			assert(0)
			if err := s.StoreReadonlyChannels(site, channels, now.Add(5*time.Second), "configured"); err != nil {
				t.Fatal(err)
			}
			assert(0)
			var copies int
			if err := db.QueryRow(`SELECT COUNT(*) FROM channel_current WHERE instance_id IN (?,?) AND channel_id=7`, a, b).Scan(&copies); err != nil || copies != 2 {
				t.Fatalf("instance readers lost inventory: %d %v", copies, err)
			}
			for _, id := range []string{a, b} {
				names, err := s.ChannelNames(id)
				if err != nil || names[7] != "c" {
					t.Fatalf("channel names missing for %s: %v %v", id, names, err)
				}
			}
			p = 12
			if err := s.ApplyChannelWrite(site, 7, nil, &p, nil, now.Add(8*time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := s.StoreReadonlyChannels(site, nil, now.Add(7*time.Second), "configured"); err != nil {
				t.Fatal(err)
			}
			assert(12)
			if err := s.UpdateReadonlyDSNForSite(site, "", now); err != nil {
				t.Fatal(err)
			}
			if ok, err := s.AcceptAgentChannelSnapshots(a); err != nil || !ok {
				t.Fatal("Agent inventory did not resume")
			}
			// Only after every collector truly loses the channel may shared state go.
			for _, id := range []string{a, b} {
				if err := s.SyncChannelSnapshotsAt(id, nil, now.Add(9*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			for _, table := range []string{"channel_base_values", "tuning_continuous_states"} {
				var n int
				if err := db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE instance_id=?", site).Scan(&n); err != nil || n != 0 {
					t.Fatalf("orphan %s=%d err=%v", table, n, err)
				}
			}
		})
	}
}
