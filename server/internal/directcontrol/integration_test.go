package directcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"controltower/internal/channelcontrol"
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
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"name":"smoke","key":"sk-secret","status":1,"weight":10,"priority":2,"group":"default"}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/channel/":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			// Real new-api rejects status on the general update endpoint
			// (source: UpdateChannel bails with MsgInvalidParams). The fake
			// must be as strict, or it green-lights broken clients.
			if _, exists := body["status"]; exists {
				_, _ = w.Write([]byte(`{"success":false,"message":"Invalid parameters"}`))
				return
			}
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
	// Twice: the second application must be a clean no-op through
	// schema_migrations.
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
	// 使用唯一后缀，避免本地长驻服务或并行测试遗留同名站点配置污染断言。
	runID := fmt.Sprintf("%d", time.Now().UnixNano())
	site, plainSite := "smoke-direct-"+runID, "smoke-queue-"+runID
	defer func() {
		// Do not leave queue-fallback fixtures for a later global expiry test.
		for _, table := range []string{"channel_commands", "tuning_recommendations", "operation_audits", "channel_current", "channel_base_values", "tuning_continuous_states", "tuning_policies"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id IN (?,?)", site, plainSite)
		}
		_, _ = db.Exec("DELETE FROM instances WHERE id IN (?,?)", site, plainSite)
	}()
	for _, id := range []string{site, plainSite} {
		if err := inner.CreateInstance(storage.Instance{ID: id, SiteID: id, Name: id, Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil && !strings.Contains(err.Error(), "Duplicate") {
			t.Fatalf("create instance %s: %v", id, err)
		}
	}
	if err := inner.StoreInstanceChannels(site, []channelcontrol.Channel{{ID: 9, Name: "smoke", Models: "m", Weight: 10, Priority: 2, Status: 1, Group: "default"}}, now); err != nil {
		t.Fatalf("seed direct channel: %v", err)
	}
	if err := inner.StoreInstanceChannels(plainSite, []channelcontrol.Channel{{ID: 5, Name: "queued", Models: "m", Weight: 1, Priority: 1, Status: 1, Group: "default"}}, now); err != nil {
		t.Fatalf("seed queued channel: %v", err)
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
	rec := tuning.Recommendation{ID: "smoke-rec-1-" + runID, InstanceID: site, ChannelID: 9, ChannelName: "smoke", CreatedAt: now, Rule: "weight_write", Evidence: map[string]any{"model": "m"}, CurrentWeight: 10, ProposedWeight: 25, ModeAtCreation: "auto"}
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
	// Automatic tuning is recorded in tuning_recommendations/commands; the
	// operation-audit feed intentionally contains only manual operations.
	if err := db.QueryRow("SELECT COUNT(*) FROM operation_audits WHERE id=?", commandID).Scan(&auditCount); err != nil || auditCount != 0 {
		t.Fatalf("automatic write leaked into manual audits: %d %v", auditCount, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM tuning_recommendations WHERE command_id=?", commandID).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("automatic write record missing: %d %v", auditCount, err)
	}

	// 2b. 直连分组写入支持规范化多分组组合，立即更新站点快照并记录新旧值。
	groupCommand, err := store.UpdateChannelGroup(ctx, site, 9, " default, vip, default ", "admin", now)
	if err != nil || groupCommand.Status != "succeeded" {
		t.Fatalf("direct group change: %#v %v", groupCommand, err)
	}
	if put := fake.lastPut(t); put["group"] != "default,vip" {
		t.Fatalf("group combination not written to new-api: %#v", put)
	}
	var currentGroup, beforeSummary, afterSummary string
	if err := db.QueryRow("SELECT group_name FROM channel_current WHERE instance_id=? AND channel_id=9", site).Scan(&currentGroup); err != nil || currentGroup != "default,vip" {
		t.Fatalf("direct group not visible in snapshot: %q %v", currentGroup, err)
	}
	if err := db.QueryRow("SELECT before_summary,after_summary FROM operation_audits WHERE id=?", groupCommand.ID).Scan(&beforeSummary, &afterSummary); err != nil || !strings.Contains(beforeSummary, `"default"`) || !strings.Contains(afterSummary, `"default,vip"`) {
		t.Fatalf("group audit missing old/new values: before=%s after=%s err=%v", beforeSummary, afterSummary, err)
	}
	// 已确认的自定义分组可以直连写入，不再依赖旧渠道快照白名单。
	if _, err := store.UpdateChannelGroup(ctx, site, 9, "custom-only", "admin", now); err != nil {
		t.Fatalf("custom direct group failed: %v", err)
	}
	fake.mu.Lock()
	putCount := len(fake.putBodies)
	fake.mu.Unlock()
	if putCount != 4 {
		t.Fatalf("custom direct group did not reach New API: put_count=%d", putCount)
	}

	// 3. Probe round runs server-side and reports like an agent round.
	probeRec := tuning.Recommendation{ID: "smoke-rec-2-" + runID, InstanceID: site, ChannelID: 9, ChannelName: "smoke", CreatedAt: now, Rule: "probe_started", Evidence: map[string]any{}, ModeAtCreation: "auto"}
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
		// 断言必须限定本次唯一站点，避免本地长驻数据中的相同渠道 ID 混入结果。
		_ = db.QueryRow("SELECT probe_attempts, probe_successes, probe_command_id FROM tuning_continuous_states WHERE instance_id=? AND channel_id=9 AND model_name='m'", site).Scan(&attempts, &successes, &pending)
		if probeStatus == "succeeded" && attempts == 3 && successes == 3 && pending == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe round did not settle: status=%s attempts=%d successes=%d pending=%v", probeStatus, attempts, successes, pending)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 4. A stale engine upsert must not resurrect the completed probe round:
	// simulate the tick race by re-persisting the pre-completion state.
	stale := tuning.ContinuousState{InstanceID: site, ChannelID: 9, ModelName: "m", KError: 1, Phase: "probing", ProbeCommandID: &probeID, UpdatedAt: now}
	if err := inner.PutContinuousState(stale); err != nil {
		t.Fatalf("stale persist: %v", err)
	}
	var attempts int
	var pending *string
	if err := db.QueryRow("SELECT probe_attempts, probe_command_id FROM tuning_continuous_states WHERE instance_id=? AND channel_id=9 AND model_name='m'", site).Scan(&attempts, &pending); err != nil {
		t.Fatalf("guard check: %v", err)
	}
	if attempts != 3 || pending != nil {
		t.Fatalf("stale upsert resurrected the completed round: attempts=%d pending=%v", attempts, pending)
	}
	// The engine's own reset (NULL command id) must still pass the guard.
	if err := inner.PutContinuousState(tuning.ContinuousState{InstanceID: site, ChannelID: 9, ModelName: "m", KError: 1, Phase: "normal", UpdatedAt: now}); err != nil {
		t.Fatalf("reset persist: %v", err)
	}
	if err := db.QueryRow("SELECT probe_attempts, probe_command_id FROM tuning_continuous_states WHERE instance_id=? AND channel_id=9 AND model_name='m'", site).Scan(&attempts, &pending); err != nil {
		t.Fatalf("reset check: %v", err)
	}
	if attempts != 0 || pending != nil {
		t.Fatalf("legitimate fold-reset must pass the guard: attempts=%d pending=%v", attempts, pending)
	}

	// 5. A site without direct config keeps the agent queue path untouched.
	queueRec := tuning.Recommendation{ID: "smoke-rec-3-" + runID, InstanceID: plainSite, ChannelID: 5, ChannelName: "queued", CreatedAt: now, Rule: "weight_write", Evidence: map[string]any{}, CurrentWeight: 1, ProposedWeight: 2, ModeAtCreation: "auto"}
	queueID, err := store.CreateContinuousWeightChange(queueRec, "system:auto", now)
	if err != nil {
		t.Fatalf("queue fallback: %v", err)
	}
	if err := db.QueryRow("SELECT status FROM channel_commands WHERE id=?", queueID).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("fallback must queue a pending command: %q %v", status, err)
	}
	queueGroup, err := store.UpdateChannelGroup(ctx, plainSite, 5, "default, vip, default", "admin", now)
	if err != nil || queueGroup.Status != "pending" {
		t.Fatalf("group queue fallback: %#v %v", queueGroup, err)
	}
	var payload string
	if err := db.QueryRow("SELECT payload_json FROM channel_commands WHERE id=?", queueGroup.ID).Scan(&payload); err != nil || !strings.Contains(payload, `"group":"default,vip"`) || !strings.Contains(payload, `"before_group":"default"`) {
		t.Fatalf("queued group payload missing normalized old/new values: %s %v", payload, err)
	}
	if _, err := store.UpdateChannelGroup(ctx, plainSite, 5, "custom-only", "admin", now); err != nil {
		t.Fatalf("custom queued group failed: %v", err)
	}

	// 6. Capacity writes use the real synchronous path, while an invalidated
	// decision must be rejected before any new-api request is made.
	policy := tuning.DefaultPolicy()
	policy.DispatchModes = map[string]string{"m": "auto"}
	if err := inner.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: policy, Mode: "observe", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE channel_base_values SET max_tpm=1000 WHERE instance_id=? AND channel_id=9`, site); err != nil {
		t.Fatal(err)
	}
	capState := tuning.ContinuousState{InstanceID: site, ChannelID: 9, ModelName: "m", Phase: "normal", ProposedWeight: 19, CapacityLimited: true, UpdatedAt: now, Capacity: tuning.CapacityControl{Initialized: true, Active: true, Fresh: true, Phase: "reducing", MaxTPM: 1000, Utilization: 1.5, SampleAt: now, ConfirmedWeight: 25, RawTarget: 15, BoundWeight: 19}}
	if err := inner.PutContinuousState(capState); err != nil {
		t.Fatal(err)
	}
	capRec := tuning.Recommendation{ID: "smoke-cap-" + runID, InstanceID: site, ChannelID: 9, CreatedAt: now, Rule: "capacity_reduce", ModeAtCreation: "auto", CurrentWeight: 25, ProposedWeight: 19, Evidence: map[string]any{"model": "m", "capacity_managed": true, "capacity": capState.Capacity}}
	beforeWrite := time.Now().UTC()
	capID, err := store.CreateContinuousWeightChange(capRec, "system:auto", now)
	if err != nil {
		t.Fatal(err)
	}
	result, appliedAt, err := inner.ContinuousCommandResult(capID)
	if err != nil || result != "succeeded" || appliedAt.Before(beforeWrite.Truncate(time.Microsecond)) {
		t.Fatalf("direct acknowledgement must include actual write time: %s %s %v", result, appliedAt, err)
	}
	if put := fake.lastPut(t); put["weight"] != float64(19) {
		t.Fatalf("capacity target not applied: %+v", put)
	}
	fake.mu.Lock()
	beforeRequests := len(fake.requests)
	fake.mu.Unlock()
	policy.DispatchModes["m"] = "off"
	if err := inner.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: policy, Mode: "observe", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	capRec.ID += "stale"
	if _, err := store.CreateContinuousWeightChange(capRec, "system:auto", now); err == nil {
		t.Fatal("mode change must invalidate capacity write")
	}
	fake.mu.Lock()
	afterRequests := len(fake.requests)
	fake.mu.Unlock()
	if afterRequests != beforeRequests {
		t.Fatal("invalid capacity decision reached new-api")
	}
}
