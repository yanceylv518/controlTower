package ingest

import (
	"testing"
	"time"

	"controltower/server/internal/agentgateway"
)

func TestExpiredMetricReportAcknowledgesOffsetAndKeepsFreshMixedBuckets(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	now := time.Now().UTC().Truncate(time.Minute)
	old := now.Add(-32 * 24 * time.Hour)
	req := agentgateway.AgentReportRequest{InstanceID: "expiry", AgentID: "agent", ReportedAt: now, LastLogID: 123, MetricBatchID: "old", AggregatedMetrics: []agentgateway.AggregatedMetricPayload{{BucketTime: old, DimensionType: "instance", DimensionKey: "expiry", RequestCount: 100}}}
	if err := svc.SaveReport(req); err != nil {
		t.Fatal(err)
	}
	if store.Offset("expiry") != 123 {
		t.Fatal("expired metrics must not block buffered report progress")
	}
	rows, _ := store.Recent1mMetrics()
	if len(rows) != 0 {
		t.Fatal("expired report was reapplied")
	}
	req.MetricBatchID = "mixed"
	req.AggregatedMetrics = append(req.AggregatedMetrics, agentgateway.AggregatedMetricPayload{BucketTime: now, DimensionType: "instance", DimensionKey: "expiry", RequestCount: 2})
	for i := 0; i < 2; i++ {
		if err := svc.SaveReport(req); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ = store.Recent1mMetrics()
	if len(rows) != 1 || rows[0].RequestCount != 2 {
		t.Fatalf("fresh mixed metrics/retry changed: %+v", rows)
	}
}
