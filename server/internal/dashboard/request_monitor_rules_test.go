package dashboard

import (
	"context"
	"controltower/internal/latencyhist"
	"controltower/server/internal/aggregator"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func channelSample(key string, count int64, ttft, duration int, at time.Time) aggregator.Metric {
	tb, db := latencyhist.BucketsV2{}, latencyhist.BucketsV2{}
	tb[ttft] = count
	db[duration] = count
	n := count
	return aggregator.Metric{InstanceID: "node-a", DimensionType: "instance_channel", DimensionKey: key, BucketTime: at, RequestCount: count, TTFTCount: &n, TTFTBuckets: &tb, LatencyBucketsV2: &db}
}
func TestChannelIndependentMetricsAndConfig(t *testing.T) {
	from := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	to := from.Add(5 * time.Minute)
	first := channelSample("partial", 100, 10, 0, from)
	broken := channelSample("partial", 100, 0, 0, from.Add(time.Minute))
	broken.TTFTBuckets = nil
	duration := channelSample("duration", 100, 0, 13, from)
	errors := channelSample("errors", 100, 0, 0, from)
	errors.ErrorCount = 20
	errors.TTFTBuckets = nil
	fast := channelSample("fast", 100, 0, 0, from)
	got := buildSlowChannels([]aggregator.Metric{first, broken, duration, errors, fast}, from, to, "volume")
	if len(got.Items) != 3 {
		t.Fatalf("%+v", got)
	}
	byKey := map[string]SlowChannel{}
	for _, c := range got.Items {
		byKey[c.Key] = c
	}
	if !byKey["partial"].Partial || byKey["partial"].TTFT.Samples != 100 || byKey["partial"].Reasons[0] != "ttft" {
		t.Fatal(byKey)
	}
	if byKey["duration"].Reasons[0] != "duration" || byKey["errors"].Reasons[0] != "error_rate" {
		t.Fatal(byKey)
	}
	rules := storage.DefaultRequestMonitorRules()
	rules.TTFTSeconds = 40
	rules.DurationSeconds = 90
	rules.ErrorPercent = 25
	got = buildSlowChannels([]aggregator.Metric{first, broken, duration, errors, fast}, from, to, "volume", rules)
	if len(got.Items) != 0 || got.PendingCount != 2 {
		t.Fatalf("%+v", got)
	}
}
func TestChannelWeightedErrorsDuplicatesAndTail(t *testing.T) {
	from := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	to := from.Add(5 * time.Minute)
	a := channelSample("weighted", 100, 0, 0, from)
	a.ErrorCount = 10
	b := channelSample("weighted", 900, 0, 0, from.Add(time.Minute))
	b.ErrorCount = 0
	rules := storage.DefaultRequestMonitorRules()
	rules.ErrorPercent = 1
	got := buildSlowChannels([]aggregator.Metric{a, b}, from, to, "volume", rules)
	if len(got.Items) != 1 || *got.Items[0].Errors.Value != 1 {
		t.Fatalf("%+v", got)
	}
	// Duplicate minute is discarded completely, never counted twice or chosen by row order.
	got = buildSlowChannels([]aggregator.Metric{a, a, b}, from, to, "volume", rules)
	if len(got.Items) != 0 {
		t.Fatal("duplicate contributed errors", got)
	}
	tail := channelSample("tail", 100, 14, 14, from)
	rules.TTFTSeconds = 120
	rules.DurationSeconds = 120
	got = buildSlowChannels([]aggregator.Metric{tail}, from, to, "volume", rules)
	if len(got.Items) != 0 || got.PendingCount != 1 || *got.Pending[0].Duration.Value != 90 || !got.Pending[0].Duration.LowerBound {
		t.Fatal(got)
	}
	// A known lower bound can establish a threshold, but must not invent a precise percentile.
	rules.DurationSeconds = 60
	got = buildSlowChannels([]aggregator.Metric{tail}, from, to, "volume", rules)
	if len(got.Items) != 1 || got.Items[0].Reasons[0] != "duration" {
		t.Fatal(got)
	}
}
func TestChannelLegacyDurationAndSamples(t *testing.T) {
	from := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	to := from.Add(5 * time.Minute)
	a := channelSample("legacy", 100, 0, 14, from)
	a.LatencyBuckets = latencyhist.DeriveV1(*a.LatencyBucketsV2)
	a.LatencyBucketsV2 = nil
	b := channelSample("legacy", 100, 0, 14, from.Add(time.Minute))
	got := buildSlowChannels([]aggregator.Metric{a, b}, from, to, "volume")
	if len(got.Items) != 1 || *got.Items[0].Duration.Value != 60 || got.Items[0].Duration.Samples != 200 {
		t.Fatal(got)
	}
	rules := storage.DefaultRequestMonitorRules()
	rules.MinSamples = 300
	got = buildSlowChannels([]aggregator.Metric{a, b}, from, to, "volume", rules)
	if len(got.Items) != 0 || got.PendingCount != 1 {
		t.Fatal(got)
	}
}
func TestChannelRulesPermissionsValidationAndConflict(t *testing.T) {
	store := ingest.NewMemoryStore()
	store.CreateInstance(storage.Instance{ID: "node-a", SiteID: "site-a"})
	handler := NewHandler(store).WithMetricSource(store).WithInstanceStore(store)
	request := func(permission, method, body string) *httptest.ResponseRecorder {
		h, c := foundationSession(t, http.HandlerFunc(handler.HandleRequestMonitorRules), "admin", []string{permission})
		r := httptest.NewRequest(method, "/api/dashboard/request-monitor/rules", strings.NewReader(body))
		r.AddCookie(c)
		r.Header.Set("X-Requested-With", "XMLHttpRequest")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	rules := storage.DefaultRequestMonitorRules()
	rules.SiteID = "site-a"
	raw, _ := json.Marshal(rules)
	if w := request("monitor.requests", "PUT", string(raw)); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request("monitor.requests", "GET", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request("settings.manage", "PUT", string(raw)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("settings.manage", "PUT", string(raw)); w.Code != 409 {
		t.Fatal(w.Code)
	}
	rules.Version = 1
	rules.WindowMinutes = 0
	raw, _ = json.Marshal(rules)
	if w := request("settings.manage", "PUT", string(raw)); w.Code != 400 {
		t.Fatal(w.Code)
	}
	saved, _ := store.LoadRequestMonitorRules(context.Background())
	if saved.Version != 1 || saved.WindowMinutes != 5 {
		t.Fatal(saved)
	}
}
func TestChannelFixedSiteIsolation(t *testing.T) {
	store := ingest.NewMemoryStore()
	for _, v := range []storage.Instance{{ID: "node-a", SiteID: "site-a"}, {ID: "node-b", SiteID: "site-b"}} {
		if err := store.CreateInstance(v); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Minute)
	a := channelSample("a", 200, 10, 0, now)
	b := channelSample("b", 999, 10, 0, now)
	b.InstanceID = "node-b"
	if err := store.Upsert1m([]aggregator.Metric{a, b}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store).WithMetricSource(store).WithInstanceStore(store)
	h, c := foundationSession(t, http.HandlerFunc(handler.HandleRequestMonitorChannels), "admin", []string{"monitor.requests"})
	request := func(query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/dashboard/request-monitor/channels"+query, nil)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request(""); w.Code != 400 {
		t.Fatal("unconfigured must not query all sites", w.Code)
	}
	rules := storage.DefaultRequestMonitorRules()
	rules.SiteID = "site-a"
	if err := store.SaveRequestMonitorRules(context.Background(), rules, "test"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"", "?site=site-b", "?instance_id=node-b"} {
		w := request(query)
		var got SlowChannels
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || len(got.Items) != 1 || got.Items[0].InstanceID != "node-a" || got.Site != "site-a" {
			t.Fatal("binding bypass", w.Code, got)
		}
	}
	rules.Version = 1
	rules.SiteID = "deleted-site"
	store.SaveRequestMonitorRules(context.Background(), rules, "test")
	w := request("")
	var got SlowChannels
	json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Items) != 0 {
		t.Fatal("deleted binding fell back to global")
	}
}
