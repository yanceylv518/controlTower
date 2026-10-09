package dashboard

import (
	"context"
	"controltower/internal/latencyhist"
	"controltower/server/internal/aggregator"
	"controltower/server/internal/alblog"
	"controltower/server/internal/ingest"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestMonitorCachePermissionsAndConfigIsolation(t *testing.T) {
	store := ingest.NewMemoryStore()
	cfg := alblog.Config{Endpoint: "cn-hangzhou.log.aliyuncs.com", Project: "project-test", Logstore: "access-log", ALBID: "alb-first", AccessKeyID: "testKey1234", SecretCipher: "synthetic-cipher"}
	if err := store.SaveALBLogConfig(context.Background(), cfg, "test"); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	handler := &RequestMonitorHandler{Store: store, Query: func(_ context.Context, c alblog.Config) alblog.Monitor {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond)
		return alblog.Monitor{Status: "success", Rows: []alblog.Minute{{Host: c.ALBID}}}
	}}
	h, cookie := foundationSession(t, handler, "admin", []string{"monitor.requests"})
	request := func() {
		r := httptest.NewRequest("GET", "/api/dashboard/request-monitor", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-cipher") {
			t.Errorf("response %d %s", w.Code, w.Body.String())
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); request() }()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("cloud query amplified", calls.Load())
	}
	cfg.Version = 1
	cfg.ALBID = "alb-second"
	if err := store.SaveALBLogConfig(context.Background(), cfg, "test"); err != nil {
		t.Fatal(err)
	}
	request()
	if calls.Load() != 2 {
		t.Fatal("reused another ALB cache")
	}
	for _, permission := range []string{"monitor.channels", "settings.manage"} {
		denied, c := foundationSession(t, handler, "admin", []string{permission})
		r := httptest.NewRequest("GET", "/api/dashboard/request-monitor", nil)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		denied.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("permission bypass", permission, w.Code)
		}
	}
}
func TestSlowChannelsHistogramRankingAndMissingData(t *testing.T) {
	from := time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)
	to := from.Add(5 * time.Minute)
	sample := func(instance, key string, count int64, index int, at time.Time) aggregator.Metric {
		n := int64(20)
		b := latencyhist.BucketsV2{}
		b[index] = 20
		duration := latencyhist.BucketsV2{}
		duration[0] = count
		return aggregator.Metric{InstanceID: instance, DimensionType: "instance_channel", DimensionKey: key, BucketTime: at, RequestCount: count, TTFTCount: &n, TTFTBuckets: &b, LatencyBucketsV2: &duration}
	}
	items := []aggregator.Metric{
		sample("i", "a", 50, 0, from), sample("i", "a", 100, 10, from.Add(time.Minute)),
		sample("i", "b", 110, 11, from), sample("i", "fast", 200, 2, from),
		sample("i", "low-volume", 99, 12, from), sample("i", "future", 999, 12, to),
	}
	missing := sample("i", "legacy", 999, 12, from)
	missing.TTFTBuckets = nil
	items = append(items, missing)
	got := buildSlowChannels(items, from, to, "volume")
	if len(got.Items) != 2 || got.Items[0].Key != "a" || got.Excluded != 1 {
		t.Fatalf("%+v", got)
	}
	// Merged 20 fast + 20 slow samples: P95 = 29s, never mean(0.2375,29.5).
	if got.Items[0].P95 != 29 || got.Items[0].Samples != 40 || got.Items[0].Trend[2] != nil {
		t.Fatal(got.Items[0])
	}
	got = buildSlowChannels(items, from, to, "latency")
	if got.Items[0].Key != "b" {
		t.Fatal("wrong latency order")
	}
	for i := 0; i < 8; i++ {
		items = append(items, sample("other", string(rune('c'+i)), int64(300+i), 12, from))
	}
	got = buildSlowChannels(items, from, to, "volume")
	if len(got.Items) != 5 {
		t.Fatal("must cap channels")
	}
}
func TestRequestMonitorUnconfiguredIsExplicit(t *testing.T) {
	h, cookie := foundationSession(t, &RequestMonitorHandler{Store: ingest.NewMemoryStore()}, "admin", []string{"monitor.requests"})
	r := httptest.NewRequest("GET", "/api/dashboard/request-monitor", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var got alblog.Monitor
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.Status != "unconfigured" || len(got.Rows) != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}
