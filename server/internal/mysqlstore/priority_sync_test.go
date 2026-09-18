package mysqlstore

import (
	"context"
	"controltower/internal/channelcontrol"
	"controltower/server/internal/tuning"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestPriorityAuthorityQueueAndSnapshots(t *testing.T) {
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
	site := fmt.Sprintf("priority-%d", time.Now().UnixNano())
	now := time.Now().UTC()
	s := New(db)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, site, site, site, now, now)
	defer func() {
		for _, table := range []string{"channel_commands", "tuning_recommendations", "operation_audits", "channel_current", "channel_base_values", "tuning_continuous_states", "tuning_policies"} {
			db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site)
		}
		db.Exec("DELETE FROM instances WHERE id=?", site)
	}()
	channels := []channelcontrol.Channel{{ID: 7, Name: "c", Models: "m", Weight: 0, Priority: 11, Status: 1}}
	if err = s.StoreInstanceChannels(site, channels, now); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE channel_base_values SET base_priority=0 WHERE instance_id=?`, site)
	// Preserve the target through model changes and temporary ineligibility.
	for i, model := range []string{"n", "m,n", "m"} {
		channels[0].Models = model
		channels[0].Priority = 99
		if err = s.StoreInstanceChannels(site, channels, now.Add(time.Duration(i+1)*time.Second)); err != nil {
			t.Fatal(err)
		}
		var target int64
		if err = db.QueryRow(`SELECT base_priority FROM channel_base_values WHERE instance_id=?`, site).Scan(&target); err != nil || target != 0 {
			t.Fatalf("lost saved priority: %d %v", target, err)
		}
	}
	setMode := func(mode string) {
		t.Helper()
		if err := s.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: tuning.Policy{DispatchModes: map[string]string{"m": mode}}, Mode: "observe", UpdatedAt: now, UpdatedBy: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	zero, current := int64(0), int64(99)
	rec := tuning.Recommendation{InstanceID: site, ChannelID: 7, Rule: "base_priority_sync", CurrentPriority: &current, ProposedPriority: &zero, ModeAtCreation: "auto", CreatedAt: now}
	for _, tc := range []struct {
		name, mode   string
		changeTarget bool
		manual       bool
		want         int
	}{{"valid", "auto", false, false, 1}, {"observe", "observe", false, false, 0}, {"off", "off", false, false, 0}, {"target changed", "auto", true, false, 0}, {"manual save in off", "off", false, true, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			exec(`DELETE FROM channel_commands WHERE instance_id=?`, site)
			exec(`UPDATE channel_base_values SET base_priority=0 WHERE instance_id=?`, site)
			setMode("auto")
			rec.ID = fmt.Sprintf("%s-%d", site, time.Now().UnixNano())
			rec.ModeAtCreation = "auto"
			if tc.manual {
				rec.ModeAtCreation = "manual"
			}
			id, err := s.CreateContinuousWeightChange(rec, "test", now)
			if err != nil {
				t.Fatal(err)
			}
			if pending, err := s.HasPendingPrioritySync(site, 7); err != nil || !pending {
				t.Fatalf("pending not detected: %v %v", pending, err)
			}
			setMode(tc.mode)
			if tc.changeTarget {
				exec(`UPDATE channel_base_values SET base_priority=12 WHERE instance_id=?`, site)
			}
			commands, err := s.ClaimPendingCommands(site, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(commands) != tc.want {
				t.Fatalf("commands=%#v", commands)
			}
			if tc.want == 1 {
				var payload map[string]any
				if err = json.Unmarshal([]byte(commands[0].PayloadJSON), &payload); err != nil {
					t.Fatal(err)
				}
				if len(payload) != 1 || payload["priority"] != float64(0) {
					t.Fatalf("priority must not alter weight: %v", payload)
				}
				exec(`UPDATE channel_commands SET updated_at=? WHERE id=?`, now.Add(-3*time.Minute), id)
				if pending, err := s.HasPendingPrioritySync(site, 7); err != nil || pending {
					t.Fatal("lost delivery prevents retry")
				}
			} else {
				var status string
				db.QueryRow(`SELECT status FROM channel_commands WHERE id=?`, id).Scan(&status)
				if status != "expired" {
					t.Fatal(status)
				}
				if err = s.CheckPrioritySync(rec); !errors.Is(err, ErrPrioritySyncSuperseded) {
					t.Fatalf("stale write allowed: %v", err)
				}
			}
		})
	}
}
