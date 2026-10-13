package directcontrol

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/channelschedule"
	"controltower/server/internal/mysqlstore"
	"controltower/server/internal/secrets"
	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
)

type scheduleHTTP struct {
	mu        sync.Mutex
	channels  map[int64]channelcontrol.Channel
	mode      string
	failReads int
	posts     int
	puts      int
	order     []int64
	entered   chan struct{}
	blocked   <-chan struct{}
}

func (f *scheduleHTTP) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer schedule-mock-token" || r.Header.Get("New-Api-User") != "7" {
		w.WriteHeader(401)
		return
	}
	if r.Method == http.MethodGet && f.failReads > 0 {
		f.failReads--
		w.WriteHeader(503)
		return
	}
	encode := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	channelMap := func(c channelcontrol.Channel) map[string]any {
		return map[string]any{"id": c.ID, "name": c.Name, "status": c.Status, "weight": c.Weight, "priority": c.Priority, "group": c.Group, "models": c.Models, "key": "mock-only"}
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/channel/" {
		items := []map[string]any{}
		ids := []int64{}
		for id := range f.channels {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			items = append(items, channelMap(f.channels[id]))
		}
		encode(map[string]any{"success": true, "data": map[string]any{"items": items, "total": len(items)}})
		return
	}
	if r.Method == http.MethodPut && r.URL.Path == "/api/channel/" {
		f.puts++
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(400)
			return
		}
		if _, exists := body["status"]; exists {
			encode(map[string]any{"success": false, "message": "status not allowed"})
			return
		}
		id := int64(body["id"].(float64))
		c := f.channels[id]
		if v, ok := body["weight"].(float64); ok {
			c.Weight = uint(v)
		}
		if v, ok := body["priority"].(float64); ok {
			c.Priority = int64(v)
		}
		if v, ok := body["group"].(string); ok {
			c.Group = v
		}
		f.channels[id] = c
		encode(map[string]any{"success": true})
		return
	}
	text := strings.TrimPrefix(r.URL.Path, "/api/channel/")
	text = strings.TrimSuffix(text, "/status")
	id, err := strconv.ParseInt(text, 10, 64)
	c, found := f.channels[id]
	if err != nil || !found {
		encode(map[string]any{"success": false, "message": "channel not found"})
		return
	}
	if r.Method == http.MethodGet {
		data := channelMap(c)
		if f.mode == "incomplete_read" {
			delete(data, "status")
		}
		encode(map[string]any{"success": true, "data": data})
		return
	}
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/status") {
		w.WriteHeader(405)
		return
	}
	f.posts++
	f.order = append(f.order, id)
	var body map[string]int
	if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || (body["status"] != 1 && body["status"] != 2) {
		w.WriteHeader(400)
		return
	}
	if f.entered != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
	}
	if f.blocked != nil {
		select {
		case <-f.blocked:
		case <-r.Context().Done():
			return
		}
	}
	switch f.mode {
	case "rejected":
		encode(map[string]any{"success": false, "message": "mock rejected"})
		return
	case "http_rejected_manual":
		c.Status = body["status"]
		f.channels[id] = c
		w.WriteHeader(403)
		return
	case "drop_before":
		connection, _, hijackErr := w.(http.Hijacker).Hijack()
		if hijackErr == nil {
			_ = connection.Close()
		}
		return
	}
	c.Status = body["status"]
	f.channels[id] = c
	if f.mode == "drop_after" {
		connection, _, hijackErr := w.(http.Hijacker).Hijack()
		if hijackErr == nil {
			_ = connection.Close()
		}
		return
	}
	encode(map[string]any{"success": true})
}

type scheduleFixture struct {
	t     *testing.T
	db    *sql.DB
	inner mysqlstore.Store
	store Store
	fake  *scheduleHTTP
	site  string
}

func newScheduleFixture(t *testing.T, channels []channelcontrol.Channel) *scheduleFixture {
	t.Helper()
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("CT_MYSQL_TEST_DSN required")
	}
	db, err := mysqlstore.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err = mysqlstore.ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	fake := &scheduleHTTP{channels: map[int64]channelcontrol.Channel{}}
	for _, c := range channels {
		fake.channels[c.ID] = c
	}
	api := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(api.Close)
	inner := mysqlstore.New(db)
	site := fmt.Sprintf("schedule-it-%d", time.Now().UnixNano())
	now := time.Now().UTC()
	if err = inner.CreateInstance(storage.Instance{ID: site, SiteID: site, Name: "本地定时测试", Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"channel_schedule_executions", "channel_schedules"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE site_id=?", site)
		}
		for _, table := range []string{"alerts", "channel_control_requests", "operation_audits", "channel_current", "channel_commands", "channel_base_values", "tuning_continuous_states", "tuning_recommendations", "tuning_policies"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site)
		}
		_, _ = db.Exec("DELETE FROM instances WHERE id=?", site)
	})
	if err = inner.StoreInstanceChannels(site, channels, now); err != nil {
		t.Fatal(err)
	}
	encrypted, err := secrets.Encrypt("schedule-mock-key", "schedule-mock-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = inner.UpdateControlConfigForSite(site, api.URL, encrypted, 7, now); err != nil {
		t.Fatal(err)
	}
	return &scheduleFixture{t, db, inner, Wrap(inner, "schedule-mock-key"), fake, site}
}

func scheduleTestChannels(status int, backup bool) []channelcontrol.Channel {
	channels := []channelcontrol.Channel{{ID: 1, Name: "测试渠道", Models: "m", Group: "default", Status: status, Weight: 10, Priority: 2}}
	if backup {
		channels = append(channels, channelcontrol.Channel{ID: 2, Name: "备用渠道", Models: "m", Group: "default", Status: 1, Weight: 20, Priority: 3})
	}
	return channels
}

func scheduleTestConfig(target int, now time.Time) channelschedule.Config {
	start, end := now.Add(time.Hour), now.Add(2*time.Hour)
	if target == 1 {
		start, end = now.Add(-time.Hour), now.Add(time.Hour)
	}
	return channelschedule.Config{Enabled: true, Days: []int{1, 2, 3, 4, 5, 6, 7}, Windows: []channelschedule.Window{{Start: start.In(channelschedule.Beijing).Format("15:04"), End: end.In(channelschedule.Beijing).Format("15:04")}}, AlignNow: true}
}

func (f *scheduleFixture) save(channel int64, cfg channelschedule.Config) channelschedule.Schedule {
	f.t.Helper()
	now := time.Now().UTC()
	_, err := f.inner.SaveChannelSchedule(context.Background(), f.site, channel, cfg, storage.OperationAudit{ID: fmt.Sprintf("cfg-%d", time.Now().UnixNano()), ActorID: "test", CreatedAt: now, UpdatedAt: now}, now)
	if err != nil {
		f.t.Fatal(err)
	}
	plan, err := f.inner.LoadChannelSchedule(context.Background(), f.site, channel)
	if err != nil {
		f.t.Fatal(err)
	}
	return plan
}

func (f *scheduleFixture) execute(plan channelschedule.Schedule) channelschedule.Schedule {
	f.t.Helper()
	if err := f.store.applyChannelSchedule(context.Background(), plan); err != nil {
		f.t.Fatal(err)
	}
	plans, err := f.inner.ListChannelSchedules(context.Background(), f.site)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, p := range plans {
		if p.ChannelID == plan.ChannelID {
			return p
		}
	}
	f.t.Fatal("plan disappeared")
	return channelschedule.Schedule{}
}

func TestChannelScheduleDirectExecutionAcrossOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		target, before, after, posts int
		backup                       bool
		mode, result                 string
		owned, alert                 bool
	}{
		{"close", 2, 1, 2, 1, true, "", "succeeded", true, false},
		{"open", 1, 2, 1, 1, true, "", "succeeded", false, false},
		{"last channel", 2, 1, 1, 0, false, "", "skipped", false, true},
		{"already manually closed", 2, 2, 2, 0, true, "", "unchanged", false, false},
		{"already open", 1, 1, 1, 0, true, "", "unchanged", false, false},
		{"business rejected", 2, 1, 1, 1, true, "rejected", "failed", false, true},
		{"HTTP rejected with concurrent manual close", 2, 1, 2, 1, true, "http_rejected_manual", "failed", false, true},
		{"lost request", 2, 1, 1, 1, true, "drop_before", "unknown", false, true},
		{"lost acknowledgement", 2, 1, 2, 1, true, "drop_after", "succeeded", true, false},
		{"incomplete channel response", 2, 1, 1, 0, true, "incomplete_read", "failed", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newScheduleFixture(t, scheduleTestChannels(tc.before, tc.backup))
			f.fake.mode = tc.mode
			plan := f.execute(f.save(1, scheduleTestConfig(tc.target, time.Now().UTC())))
			if plan.Execution == nil || plan.Execution.Status != tc.result || plan.ClosedBySchedule != tc.owned {
				t.Fatalf("plan: %+v run: %+v", plan, plan.Execution)
			}
			f.fake.mu.Lock()
			actual, posts, puts := f.fake.channels[1], f.fake.posts, f.fake.puts
			f.fake.mu.Unlock()
			if actual.Status != tc.after || actual.Weight != 10 || actual.Priority != 2 || actual.Group != "default" || posts != tc.posts || puts != 0 {
				t.Fatalf("actual=%+v posts=%d puts=%d", actual, posts, puts)
			}
			var firing int
			if err := f.db.QueryRow("SELECT COUNT(*) FROM alerts WHERE instance_id=? AND rule_key='channel_schedule' AND status='firing'", f.site).Scan(&firing); err != nil {
				t.Fatal(err)
			}
			if (firing > 0) != tc.alert {
				t.Fatalf("alerts=%d", firing)
			}
			var auditStatus string
			if err := f.db.QueryRow("SELECT status FROM operation_audits WHERE id=?", plan.Execution.ID).Scan(&auditStatus); err != nil || auditStatus != tc.result {
				t.Fatalf("audit=%q err=%v", auditStatus, err)
			}
			if tc.posts > 0 {
				life, err := f.inner.QueryChannelLifeline(context.Background(), tuning.ProbeRecordQuery{Site: f.site, ChannelID: 1, From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Minute), Now: time.Now(), Limit: 50}, "operations")
				if err != nil || len(life.Items) != 1 {
					t.Fatalf("lifeline=%+v err=%v", life, err)
				}
				if tc.result == "succeeded" && life.Items[0].Outcome != "succeeded" {
					t.Fatalf("confirmed write remains uncertain: %+v", life.Items[0])
				}
			}
			// 终态不再覆盖本边界后的人工操作，也不重放未知结果。
			f.fake.mu.Lock()
			c := f.fake.channels[1]
			c.Status = tc.before
			f.fake.channels[1] = c
			f.fake.mu.Unlock()
			f.execute(plan)
			f.fake.mu.Lock()
			posts = f.fake.posts
			f.fake.mu.Unlock()
			if posts != tc.posts {
				t.Fatalf("boundary replayed: %d", posts)
			}
		})
	}
}

func TestChannelScheduleOrdinaryOpenClearsStaleProbeSpeed(t *testing.T) {
	f := newScheduleFixture(t, scheduleTestChannels(2, true))
	now := time.Now().UTC()
	if err := f.inner.PutContinuousState(tuning.ContinuousState{InstanceID: f.site, ChannelID: 1, ModelName: "m", Phase: "normal", UpdatedAt: now, ProbeSpeed: tuning.ProbeSpeedStats{Ready: true, Factor: 1.2, Samples: 50, MeanSeconds: 0.1, LastStartedAt: now.Add(-time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	f.execute(f.save(1, scheduleTestConfig(1, now)))
	states, err := f.inner.ListContinuousStates(f.site)
	if err != nil || len(states) != 1 || states[0].ProbeSpeed.Ready || states[0].ProbeSpeed.Samples != 0 {
		t.Fatalf("stale probe speed survived opening: %+v err=%v", states, err)
	}
}

func TestChannelScheduleNextBoundaryIsDueWithoutPollingDrift(t *testing.T) {
	f := newScheduleFixture(t, scheduleTestChannels(2, true))
	boundary := time.Now().UTC().Truncate(time.Minute).Add(2 * time.Minute)
	config := channelschedule.Config{Enabled: true, Days: []int{1, 2, 3, 4, 5, 6, 7}, Windows: []channelschedule.Window{{Start: boundary.In(channelschedule.Beijing).Format("15:04"), End: boundary.Add(time.Minute).In(channelschedule.Beijing).Format("15:04")}}}
	plan := f.save(1, config)
	run, err := f.inner.PrepareChannelSchedule(context.Background(), plan, boundary.Add(-10*time.Second))
	if err != nil || run != nil {
		t.Fatalf("premature boundary: run=%+v err=%v", run, err)
	}
	var due time.Time
	if err := f.db.QueryRow("SELECT next_check_at FROM channel_schedules WHERE site_id=? AND channel_id=1", f.site).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if !due.Equal(boundary) {
		t.Fatalf("next check drifted past boundary: got=%s want=%s", due, boundary)
	}
}

func TestChannelScheduleManualOverrideChangesOriginUntilNextOpening(t *testing.T) {
	f := newScheduleFixture(t, scheduleTestChannels(1, true))
	now := time.Now().UTC()
	plan := f.execute(f.save(1, scheduleTestConfig(2, now)))
	if !plan.ClosedBySchedule {
		t.Fatal("scheduled close did not record its origin")
	}
	if _, err := f.store.UpdateChannelStatus(context.Background(), f.site, 1, 2, "test:manual", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	plan = f.execute(plan)
	states, err := f.inner.ListContinuousStates(f.site)
	if err != nil || plan.ClosedBySchedule || len(states) != 1 || states[0].PausedReason != tuning.ManualDisabled {
		t.Fatalf("manual ownership lost at a completed boundary: plan=%+v states=%+v err=%v", plan, states, err)
	}
	cfg := scheduleTestConfig(1, now)
	cfg.Revision = plan.Revision
	plan = f.execute(f.save(1, cfg))
	states, err = f.inner.ListContinuousStates(f.site)
	if err != nil || plan.Execution.Status != "succeeded" || plan.ClosedBySchedule || len(states) != 1 || states[0].PausedReason != "" {
		t.Fatalf("scheduled opening retained manual ownership: plan=%+v states=%+v err=%v", plan, states, err)
	}
}

func TestChannelScheduleAndManualStatusShareSiteAndChannelGates(t *testing.T) {
	f := newScheduleFixture(t, scheduleTestChannels(1, true))
	plan := f.save(1, scheduleTestConfig(2, time.Now().UTC()))
	siteRelease, err := f.inner.LockChannelScheduleSite(context.Background(), f.site, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, err = f.store.UpdateChannelStatus(ctx, f.site, 1, 2, "test:manual", time.Now().UTC())
	cancel()
	siteRelease()
	if !errors.Is(err, tuning.ErrChannelControlBusy) {
		t.Fatalf("manual write crossed schedule site gate: %v", err)
	}
	channelRelease, err := f.inner.LockChannelWrite(context.Background(), f.site, 1)
	if err != nil {
		t.Fatal(err)
	}
	err = f.store.applyChannelSchedule(context.Background(), plan)
	channelRelease()
	if !errors.Is(err, channelschedule.ErrBusy) {
		t.Fatalf("schedule crossed manual channel gate: %v", err)
	}
	if result := f.execute(plan); result.Execution.Status != "succeeded" {
		t.Fatalf("schedule did not resume after both gates released: %+v", result)
	}
}

func TestChannelScheduleRealWorkerOpensBeforeClosing(t *testing.T) {
	channels := scheduleTestChannels(1, true)
	channels[1].Status = 2
	f := newScheduleFixture(t, channels)
	f.save(1, scheduleTestConfig(2, time.Now().UTC()))
	f.save(2, scheduleTestConfig(1, time.Now().UTC()))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); f.store.RunChannelSchedules(ctx) }()
	deadline := time.Now().Add(8 * time.Second)
	for {
		plans, err := f.inner.ListChannelSchedules(context.Background(), f.site)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		complete := len(plans) == 2
		for _, plan := range plans {
			complete = complete && plan.Execution != nil && plan.Execution.Status == "succeeded"
		}
		if complete {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("worker did not finish: %+v", plans)
		}
		time.Sleep(30 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker failed to stop")
	}
	f.fake.mu.Lock()
	defer f.fake.mu.Unlock()
	if len(f.fake.order) != 2 || f.fake.order[0] != 2 || f.fake.order[1] != 1 || f.fake.channels[1].Status != 2 || f.fake.channels[2].Status != 1 {
		t.Fatalf("worker order=%v channels=%v", f.fake.order, f.fake.channels)
	}
}

func TestChannelScheduleReadRetriesAreBounded(t *testing.T) {
	f := newScheduleFixture(t, scheduleTestChannels(1, true))
	f.fake.failReads = 10
	plan := f.save(1, scheduleTestConfig(2, time.Now().UTC()))
	for attempt := 1; attempt <= channelschedule.MaxAttempts; attempt++ {
		plan = f.execute(plan)
		want := "retry"
		if attempt == channelschedule.MaxAttempts {
			want = "failed"
		}
		if plan.Execution.Status != want || plan.Execution.Attempts != attempt {
			t.Fatalf("attempt %d: %+v", attempt, plan.Execution)
		}
		if attempt == 1 {
			remaining := f.fake.failReads
			plan = f.execute(plan)
			if f.fake.failReads != remaining || plan.Execution.Attempts != 1 {
				t.Fatal("retry ignored backoff")
			}
		}
		if _, err := f.db.Exec("UPDATE channel_schedule_executions SET updated_at=? WHERE id=?", time.Now().Add(-20*time.Minute), plan.Execution.ID); err != nil {
			t.Fatal(err)
		}
	}
	if f.fake.posts != 0 {
		t.Fatal("read failure attempted writes")
	}
}

func TestChannelScheduleWaitsForRecoveryAndStopsAtBudget(t *testing.T) {
	f := newScheduleFixture(t, scheduleTestChannels(2, true))
	now := time.Now().UTC()
	if err := f.inner.SaveChannelBaseValues(f.site, "test", []tuning.ChannelBaseValue{{ChannelID: 1, ModelName: "m", BaseWeight: 100, BasePriority: 2}}, now); err != nil {
		t.Fatal(err)
	}
	policy := tuning.DefaultPolicy()
	policy.DispatchModes = map[string]string{"m": "auto"}
	if err := f.inner.PutPolicy(tuning.PolicyRecord{InstanceID: f.site, Policy: policy, Mode: "observe", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := f.inner.PutContinuousState(tuning.ContinuousState{InstanceID: f.site, ChannelID: 1, ModelName: "m", Phase: "circuit", CircuitDisabled: true, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	plan := f.execute(f.save(1, scheduleTestConfig(1, now)))
	if plan.Execution.Status != "waiting_recovery" || f.fake.posts != 0 {
		t.Fatalf("forced circuit open: %+v", plan.Execution)
	}
	if err := f.inner.PutContinuousState(tuning.ContinuousState{InstanceID: f.site, ChannelID: 1, ModelName: "m", Phase: "normal", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	plan = f.execute(plan)
	if plan.Execution.Status != "succeeded" || f.fake.posts != 1 {
		t.Fatalf("recovered: %+v", plan.Execution)
	}

	f = newScheduleFixture(t, scheduleTestChannels(2, true))
	now = time.Now().UTC()
	if err := f.inner.PutContinuousState(tuning.ContinuousState{InstanceID: f.site, ChannelID: 1, ModelName: "m", Phase: "normal", Capacity: tuning.CapacityControl{PendingCommandID: "pending-operation"}, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	cfg := scheduleTestConfig(1, now)
	if _, err := f.inner.SaveChannelSchedule(context.Background(), f.site, 1, cfg, storage.OperationAudit{ID: "old-" + f.site, ActorID: "test"}, now.Add(-11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	plan, err := f.inner.LoadChannelSchedule(context.Background(), f.site, 1)
	if err != nil {
		t.Fatal(err)
	}
	plan = f.execute(plan)
	if plan.Execution.Status != "waiting_control" {
		t.Fatalf("pending write: %+v", plan.Execution)
	}
	plan = f.execute(plan)
	if plan.Execution.Status != "failed" || f.fake.posts != 0 {
		t.Fatalf("waiting exceeded budget: %+v", plan.Execution)
	}
}

func TestChannelScheduleRejectsExpiredBoundaryAndUnsupportedControl(t *testing.T) {
	f := newScheduleFixture(t, scheduleTestChannels(1, true))
	plan := f.save(1, scheduleTestConfig(2, time.Now().UTC()))
	run, err := f.inner.PrepareChannelSchedule(context.Background(), plan, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec("UPDATE channel_schedule_executions SET expires_at=? WHERE id=?", time.Now().Add(-time.Second), run.ID); err != nil {
		t.Fatal(err)
	}
	plan = f.execute(plan)
	if plan.Execution.Status != "superseded" || f.fake.posts != 0 {
		t.Fatalf("expired write: %+v", plan.Execution)
	}
	if err = f.inner.UpdateControlConfigForSite(f.site, "", "", 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	cfg := plan.Config
	cfg.AlignNow = true
	if _, err = f.inner.SaveChannelSchedule(context.Background(), f.site, 1, cfg, storage.OperationAudit{}, time.Now()); !errors.Is(err, channelschedule.ErrUnsupported) {
		t.Fatalf("unsupported control: %v", err)
	}
}

func TestChannelScheduleCloseWaitsForOpeningInAnotherBatch(t *testing.T) {
	channels := scheduleTestChannels(1, true)
	channels[1].Status = 2
	f := newScheduleFixture(t, channels)
	closePlan := f.save(1, scheduleTestConfig(2, time.Now().UTC()))
	openPlan := f.save(2, scheduleTestConfig(1, time.Now().UTC()))
	closePlan = f.execute(closePlan)
	if closePlan.Execution.Status != "waiting_control" || f.fake.posts != 0 {
		t.Fatalf("close before backup: %+v", closePlan.Execution)
	}
	f.execute(openPlan)
	closePlan = f.execute(closePlan)
	if closePlan.Execution.Status != "succeeded" || len(f.fake.order) != 2 || f.fake.order[0] != 2 || f.fake.order[1] != 1 {
		t.Fatalf("run=%+v order=%v", closePlan.Execution, f.fake.order)
	}
}

func TestChannelScheduleTwoWorkersAndConfigurationLock(t *testing.T) {
	f := newScheduleFixture(t, scheduleTestChannels(1, true))
	plan := f.save(1, scheduleTestConfig(2, time.Now().UTC()))
	blocked := make(chan struct{})
	f.fake.blocked = blocked
	f.fake.entered = make(chan struct{}, 1)
	finished := make(chan error, 1)
	go func() { finished <- f.store.applyChannelSchedule(context.Background(), plan) }()
	select {
	case <-f.fake.entered:
	case <-time.After(5 * time.Second):
		close(blocked)
		t.Fatal("write did not start")
	}
	if err := f.store.applyChannelSchedule(context.Background(), plan); !errors.Is(err, channelschedule.ErrBusy) {
		close(blocked)
		t.Fatalf("second worker: %v", err)
	}
	cfg := plan.Config
	cfg.Enabled = false
	_, err := f.inner.SaveChannelSchedule(context.Background(), f.site, 1, cfg, storage.OperationAudit{}, time.Now())
	if !errors.Is(err, channelschedule.ErrBusy) {
		close(blocked)
		t.Fatalf("edit during write: %v", err)
	}
	close(blocked)
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	f.fake.mu.Lock()
	posts := f.fake.posts
	f.fake.mu.Unlock()
	if posts != 1 {
		t.Fatalf("duplicate writers: %d", posts)
	}
}

func TestChannelScheduleSiteGateKeepsOrdinaryWritesConcurrent(t *testing.T) {
	f := newScheduleFixture(t, scheduleTestChannels(1, true))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := f.inner.LockChannelControlSite(ctx, f.site, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.inner.LockChannelControlSite(ctx, f.site, 0)
	if err != nil {
		first()
		t.Fatalf("ordinary peer blocked: %v", err)
	}
	if release, err := f.inner.LockChannelScheduleSite(ctx, f.site, 0); !errors.Is(err, channelschedule.ErrBusy) {
		if release != nil {
			release()
		}
		first()
		second()
		t.Fatalf("schedule crossed ordinary writes: %v", err)
	}
	first()
	second()
	exclusive, err := f.inner.LockChannelScheduleSite(ctx, f.site, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer exclusive()
	if release, err := f.inner.LockChannelControlSite(ctx, f.site, 0); !errors.Is(err, channelschedule.ErrBusy) {
		if release != nil {
			release()
		}
		t.Fatalf("ordinary write crossed schedule: %v", err)
	}
}

func TestChannelScheduleVersionNoopDisableDeleteAndRestart(t *testing.T) {
	f := newScheduleFixture(t, scheduleTestChannels(1, true))
	plan := f.save(1, scheduleTestConfig(2, time.Now().UTC()))
	plan = f.execute(plan)
	cfg := plan.Config
	cfg.AlignNow = false
	cfg.ExpectedUpdatedAt = &plan.UpdatedAt
	unchanged := f.save(1, cfg)
	if unchanged.Revision != plan.Revision || !unchanged.EffectiveAt.Equal(plan.EffectiveAt) {
		t.Fatal("no-op reset execution version")
	}
	stale := cfg
	stale.Revision--
	if _, err := f.inner.SaveChannelSchedule(context.Background(), f.site, 1, stale, storage.OperationAudit{}, time.Now()); !errors.Is(err, channelschedule.ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	cfg.Enabled = false
	disabled := f.save(1, cfg)
	if err := f.store.applyChannelSchedule(context.Background(), disabled); err != nil {
		t.Fatal(err)
	}
	if f.fake.channels[1].Status != 2 || f.fake.posts != 1 {
		t.Fatal("disabling changed upstream status")
	}
	if err := f.inner.DeleteChannelSchedule(context.Background(), f.site, 1, plan.Revision, &plan.UpdatedAt, storage.OperationAudit{}, time.Now()); !errors.Is(err, channelschedule.ErrConflict) {
		t.Fatalf("stale delete: %v", err)
	}
	if err := f.inner.DeleteChannelSchedule(context.Background(), f.site, 1, disabled.Revision, &disabled.UpdatedAt, storage.OperationAudit{ID: "delete-" + f.site, ActorID: "test"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if f.fake.channels[1].Status != 2 || f.fake.posts != 1 {
		t.Fatal("deleting changed upstream status")
	}
	newPlan := f.save(1, scheduleTestConfig(1, time.Now().UTC()))
	if err := f.inner.DeleteChannelSchedule(context.Background(), f.site, 1, newPlan.Revision, &plan.UpdatedAt, storage.OperationAudit{}, time.Now()); !errors.Is(err, channelschedule.ErrConflict) {
		t.Fatalf("recreated plan accepted old timestamp: %v", err)
	}
	// 模拟服务错过最近边界，恢复后只补当前目标。
	if _, err := f.db.Exec("UPDATE channel_schedules SET effective_at=?,config_json=JSON_SET(config_json,'$.align_now',false) WHERE site_id=?", time.Now().Add(-8*24*time.Hour), f.site); err != nil {
		t.Fatal(err)
	}
	newPlan, err := f.inner.LoadChannelSchedule(context.Background(), f.site, 1)
	if err != nil {
		t.Fatal(err)
	}
	newPlan = f.execute(newPlan)
	if newPlan.Execution.Status != "succeeded" || f.fake.posts != 2 || f.fake.channels[1].Status != 1 {
		t.Fatalf("restart: %+v", newPlan.Execution)
	}
}
