package ingest

import (
	"controltower/server/internal/agentgateway"
	"controltower/server/internal/storage"
	"testing"
	"time"
)

func TestNginxTimingReportUpsertsAndPrunes(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	at := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)
	report := agentgateway.AgentReportRequest{InstanceID: "i", AgentID: "a", ReportedAt: time.Now(), NginxTimingBuckets: []agentgateway.NginxTimingBucketPayload{{BucketAt: at, RequestCount: 1}}, NginxSlowSamples: []agentgateway.NginxSlowSamplePayload{{OccurredAt: at, Path: "/v1/chat", Status: 504, RT: 12, RequestID: "req-1"}}}
	if err := svc.SaveReport(report); err != nil {
		t.Fatal(err)
	}
	report.NginxTimingBuckets[0].RequestCount = 9
	if err := svc.SaveReport(report); err != nil {
		t.Fatal(err)
	}
	items, _ := store.QueryNginxTiming(storage.NginxTimingQuery{InstanceID: "i", Since: at.Add(-time.Minute)})
	if len(items) != 1 || items[0].RequestCount != 9 {
		t.Fatalf("items=%#v", items)
	}
	samples, _ := store.QueryNginxSlowSamples(storage.NginxSlowSampleQuery{InstanceID: "i", Since: at.Add(-time.Minute), Limit: 10})
	if len(samples) != 1 || samples[0].RequestID != "req-1" {
		t.Fatalf("samples=%#v", samples)
	}
	if n, e := store.PruneBefore("nginx_timing_1m", time.Now()); e != nil || n != 1 {
		t.Fatalf("prune bucket n=%d err=%v", n, e)
	}
	if n, e := store.PruneBefore("nginx_slow_samples", time.Now()); e != nil || n != 1 {
		t.Fatalf("prune samples n=%d err=%v", n, e)
	}
}
