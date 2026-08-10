package directcontrol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"controltower/server/internal/mysqlstore"
	"controltower/server/internal/secrets"
	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
)

// fakeNewAPI mimics the slice of the new-api admin API the direct path uses
// and records every call for assertions.
type fakeNewAPI struct {
	mu        sync.Mutex
	requests  []string
	putBodies []map[string]any
}

func (f *fakeNewAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer smoke-token" || r.Header.Get("New-Api-User") != "7" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/channel/test/"):
			_, _ = w.Write([]byte(`{"success":true,"message":"","time":0.5}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/channel/9":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"name":"smoke","key":"sk-secret","status":1,"weight":10,"priority":2}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/channel/":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.putBodies = append(f.putBodies, body)
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/channel/":
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0}}`))
		default:
			http.NotFound(w, r)
		}
	})
}

func (f *fakeNewAPI) lastPut(t *testing.T) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.putBodies) == 0 {
		t.Fatal("no PUT reached fake new-api")
	}
	return f.putBodies[len(f.putBodies)-1]
}

// TestDirectControlIntegration drives the full direct path against a real
// MySQL/MariaDB (CT_MYSQL_TEST_DSN) and a fake new-api: config storage with
// encryption, preflight, weight write, probe round, and the queue fallback
// for a site without direct config.
func TestDirectControlIntegration(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN to run the direct control integration test")
	}
	db, err := mysqlstore.Open(dsn)
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// Twice: 041 must replay cleanly on every startup like all migrations.
	if err := mysqlstore.ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if err := mysqlstore.ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatalf("replay migrations: %v", err)
	}

	fake := &fakeNewAPI{}
	api := httptest.NewServer(fake.handler())
	defer api.Close()

	inner := mysqlstore.New(db)
	now := time.Now().UTC()
	site, plainSite := "smoke-direct", "smoke-queue"
	for _, id := range []string{site, plainSite} {
		if err := inner.CreateInstance(storage.Instance{ID: id, SiteID: id, Name: id, Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil && !strings.Contains(err.Error(), "Duplicate") {
			t.Fatalf("create instance %s: %v", id, err)
		}
	}
	encrypted, err := secrets.Encrypt("smoke-key", "smoke-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := inner.UpdateControlConfigForSite(site, api.URL, encrypted, 7, now); err != nil {
		t.Fatalf("store control config: %v", err)
	}

	store := Wrap(inner, "smoke-key")

	// 1. Preflight executes synchronously and lands terminal.
	command, err := store.CreateTuningPreflight(site, 9, "admin", now)
	if err != nil || command.Status != "succeeded" {
		t.Fatalf("direct preflight: %#v %v", command, err)
	}
	if got, found, err := inner.GetTuningPreflight(site, command.ID); err != nil || !found || got.Status != "succeeded" {
		t.Fatalf("preflight poll: %#v %v %v", got, found, err)
	}
	put := fake.lastPut(t)
	if put["weight"] != float64(10) || put["priority"] != float64(2) {
		t.Fatalf("preflight must PUT unchanged fields: %#v", put)
	}
	if _, leaked := put["key"]; leaked {
		t.Fatalf("preflight PUT leaked channel key: %#v", put)
	}

	// 2. Direct weight write hits new-api and leaves the full paper trail.
	rec := tuning.Recommendation{ID: "smoke-rec-1", InstanceID: site, ChannelID: 9, ChannelName: "smoke", CreatedAt: now, Rule: "weight_write", Evidence: map[string]any{"model": "m"}, CurrentWeight: 10, ProposedWeight: 25, ModeAtCreation: "auto"}
	commandID, err := store.CreateContinuousWeightChange(rec, "system:auto", now)
	if err != nil {
		t.Fatalf("direct weight change: %v", err)
	}
	if put := fake.lastPut(t); put["weight"] != float64(25) {
		t.Fatalf("weight not written to new-api: %#v", put)
	}
	var status string
	if err := db.QueryRow("SELECT status FROM channel_commands WHERE id=?", commandID).Scan(&status); err != nil || status != "succeeded" {
		t.Fatalf("direct command row must be terminal: %q %v", status, err)
	}
	var auditCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM operation_audits WHERE id=?", commandID).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("audit row missing: %d %v", auditCount, err)
	}

	// 3. Probe round runs server-side and reports like an agent round.
	probeRec := tuning.Recommendation{ID: "smoke-rec-2", InstanceID: site, ChannelID: 9, ChannelName: "smoke", CreatedAt: now, Rule: "probe_started", Evidence: map[string]any{}, ModeAtCreation: "auto"}
	probeID, err := store.CreateContinuousProbe(probeRec, "m", 3, 1, now)
	if err != nil {
		t.Fatalf("direct probe: %v", err)
	}
	if err := inner.PutContinuousState(tuning.ContinuousState{InstanceID: site, ChannelID: 9, ModelName: "m", KError: 1, Phase: "probing", ProbeCommandID: &probeID, UpdatedAt: now}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		var probeStatus string
		var attempts, successes int
		var pending *string
		if err := db.QueryRow("SELECT status FROM channel_commands WHERE id=?", probeID).Scan(&probeStatus); err != nil {
			t.Fatalf("probe command: %v", err)
		}
		_ = db.QueryRow("SELECT probe_attempts, probe_successes, probe_command_id FROM tuning_continuous_states WHERE channel_id=9 AND model_name='m'").Scan(&attempts, &successes, &pending)
		if probeStatus == "succeeded" && attempts == 3 && successes == 3 && pending == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe round did not settle: status=%s attempts=%d successes=%d pending=%v", probeStatus, attempts, successes, pending)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 4. A site without direct config keeps the agent queue path untouched.
	queueRec := tuning.Recommendation{ID: "smoke-rec-3", InstanceID: plainSite, ChannelID: 5, ChannelName: "queued", CreatedAt: now, Rule: "weight_write", Evidence: map[string]any{}, CurrentWeight: 1, ProposedWeight: 2, ModeAtCreation: "auto"}
	queueID, err := store.CreateContinuousWeightChange(queueRec, "system:auto", now)
	if err != nil {
		t.Fatalf("queue fallback: %v", err)
	}
	if err := db.QueryRow("SELECT status FROM channel_commands WHERE id=?", queueID).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("fallback must queue a pending command: %q %v", status, err)
	}
}
