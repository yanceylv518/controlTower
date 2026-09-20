package mysqlstore

import (
	"context"
	"controltower/server/internal/voicealert"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type trialTestSource struct {
	logs []voicealert.TrialLog
	fail bool
}

func (s *trialTestSource) TrialHead(context.Context, string) (int64, error) { return 100, nil }
func (s *trialTestSource) TrialLogs(context.Context, string, int64) ([]voicealert.TrialLog, error) {
	if s.fail {
		return nil, errors.New("offline")
	}
	return s.logs, nil
}
func (s *trialTestSource) TrialIdentities(_ context.Context, _ string, user int64) ([]voicealert.TrialIdentity, error) {
	if s.fail {
		return nil, errors.New("offline")
	}
	if user > 0 {
		return []voicealert.TrialIdentity{{ID: 9, Name: "test-key"}}, nil
	}
	return []voicealert.TrialIdentity{{ID: 7, Name: "test-user"}}, nil
}

type trialTestCaller struct{ calls atomic.Int32 }

func (c *trialTestCaller) Ready() bool { return true }
func (c *trialTestCaller) CallTemplate(_ context.Context, _ voicealert.Config, _ voicealert.Target, _ string, _ map[string]string) voicealert.Result {
	c.calls.Add(1)
	return voicealert.Result{Status: "unknown", Code: "simulated_timeout"}
}

func TestTrialFollowupIntegration(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated local MySQL")
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
	site := fmt.Sprintf("trial-test-%d", time.Now().UnixNano())
	now := time.Now().UTC()
	store := voicealert.Store{DB: db}
	people := []voicealert.Person{}
	defer func() {
		for _, table := range []string{"trial_deliveries", "trial_events", "trial_watches", "trial_site_state", "voice_alert_calls", "voice_alert_site_config"} {
			db.Exec("DELETE FROM "+table+" WHERE site_id=?", site)
		}
		for _, p := range people {
			db.Exec("DELETE FROM operations_people WHERE id=?", p.ID)
			db.Exec("DELETE FROM operation_audits WHERE target_id=?", p.ID)
		}
		db.Exec("DELETE FROM instances WHERE id=?", site)
		db.Exec("DELETE FROM operation_audits WHERE target_id=?", site)
	}()
	if _, err = db.Exec(`INSERT INTO instances(id,name,site_id,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, site, site, site, now, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		phone := fmt.Sprintf("139%08d", (time.Now().UnixNano()+int64(i))%100000000)
		p, e := store.SavePerson(ctx, voicealert.Person{Name: fmt.Sprintf("测试运营%d", i), Phone: phone, Enabled: true}, "test")
		if e != nil {
			t.Fatal(e)
		}
		people = append(people, p)
	}
	source := &trialTestSource{}
	caller := &trialTestCaller{}
	cfg := voicealert.DefaultConfig()
	cfg.Enabled = true
	cfg.TtsCode = "TTS_tpm"
	cfg.TrialTtsCode = "TTS_trial"
	cfg.TrialTemplateReady = true
	cfg.Recipients = []voicealert.Recipient{{Phone: people[0].Phone}}
	if err = store.SaveConfig(ctx, site, cfg, "test"); err != nil {
		t.Fatal(err)
	}
	input := voicealert.TrialWatch{Site: site, UserID: 7, Label: "测试客户", Rule: "first", GapMinutes: 30, IncludeFailed: true, Phone: true, Message: true, PersonIDs: []string{people[0].ID, people[1].ID}, Enabled: true}
	first, err := store.SaveWatch(ctx, input, "test", source)
	if err != nil {
		t.Fatal(err)
	}
	input.TokenID = 9
	second, err := store.SaveWatch(ctx, input, "test", source)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec("DELETE FROM operation_audits WHERE target_id IN (?,?)", first.ID, second.ID)
	var messages atomic.Int32
	runner := &voicealert.TrialRunner{Store: store, Source: source, Caller: caller, Message: func(context.Context, voicealert.TrialEvent) (string, error) { messages.Add(1); return "sent", nil }}
	source.logs = []voicealert.TrialLog{{ID: 99, UserID: 7, TokenID: 9, Type: 2, CreatedAt: now.Add(-time.Hour)}, {ID: 101, UserID: 7, TokenID: 9, Type: 5, CreatedAt: time.Now().UTC().Add(time.Second)}}
	var group sync.WaitGroup
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if e := runner.Once(ctx); e != nil {
				t.Error(e)
			}
		}()
	}
	group.Wait()
	if caller.calls.Load() != 2 || messages.Load() != 1 {
		t.Fatalf("duplicate or missing dispatch: phone=%d message=%d", caller.calls.Load(), messages.Load())
	}
	if err = runner.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if caller.calls.Load() != 2 {
		t.Fatal("unknown calls retried")
	}
	events, err := store.TrialEvents(ctx, site)
	if err != nil || len(events) != 2 {
		t.Fatal(events, err)
	}
	if len(events[0].Deliveries) != 3 || len(events[1].Deliveries) != 3 {
		t.Fatal("overlap subscriptions must share delivery outcomes")
	}
	original := people[0].Name
	people[0].Name = "已改名"
	people[0], err = store.SavePerson(ctx, people[0], "test")
	if err != nil {
		t.Fatal(err)
	}
	events, err = store.TrialEvents(ctx, site)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range events[0].Deliveries {
		if d.Name == original {
			found = true
		}
	}
	if !found {
		t.Fatal("historical snapshot changed")
	}
	// Traffic calls share the same phone ledger even when trial results are unknown.
	id, err := store.Claim(ctx, voicealert.Target{Site: site, UserID: 7, Phone: people[0].Phone}, time.Now().UTC(), 0, 20000000, "上涨", time.Now().UTC())
	if err != nil || id != "" {
		t.Fatal("cross-type quota bypass", id, err)
	}
	// Pausing remains possible during a source outage; stale editors cannot overwrite.
	source.fail = true
	first.Enabled = false
	paused, err := store.SaveWatch(ctx, first, "test", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveWatch(ctx, first, "test", source); !errors.Is(err, voicealert.ErrConflict) {
		t.Fatal("missing revision conflict", err)
	}
	source.fail = false
	paused.Reset = true
	paused.Enabled = true
	paused.PersonIDs = []string{people[0].ID}
	_, err = store.SaveWatch(ctx, paused, "test", source)
	if err != nil {
		t.Fatal(err)
	}
	off := false
	cfg.ServiceEnabled = &off
	if err = store.SaveConfig(ctx, site, cfg, "test"); err != nil {
		t.Fatal(err)
	}
	source.logs = []voicealert.TrialLog{{ID: 102, UserID: 7, TokenID: 9, Type: 2, CreatedAt: time.Now().UTC().Add(2 * time.Second)}}
	if err = runner.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if caller.calls.Load() != 2 {
		t.Fatal("disabled service called")
	}
	events, err = store.TrialEvents(ctx, site)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, d := range events[0].Deliveries {
		if d.Status == "service_disabled" {
			found = true
		}
	}
	if !found {
		t.Fatal("disabled service outcome missing")
	}
}
