package ingest

import (
	"encoding/json"
	"testing"
	"time"

	"controltower/server/internal/agentgateway"
)

func TestTPMOnlyTrafficSurvivesIngestRetryAndRollup(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	var sparse agentgateway.AggregatedMetricPayload
	if err := json.Unmarshal([]byte(`{"bucket_time":"2026-09-14T10:00:00Z","window_seconds":60,"dimension_type":"instance_user_channel","dimension_key":"site:user:7:channel:3","tpm":30}`), &sparse); err != nil {
		t.Fatal(err)
	}
	for _, batch := range []string{"first", "second", "first"} {
		report := agentgateway.AgentReportRequest{InstanceID: "site", AgentID: "test", ReportedAt: sparse.BucketTime.Add(time.Minute), MetricBatchID: batch, AggregatedMetrics: []agentgateway.AggregatedMetricPayload{sparse}}
		if err := service.SaveReport(report); err != nil {
			t.Fatal(err)
		}
	}
	for _, window := range []string{"1m", "5m"} {
		metrics, err := store.QueryMetricHistory(window, sparse.DimensionType, sparse.DimensionKey, sparse.BucketTime.Add(-time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if len(metrics) != 1 {
			t.Fatalf("%s: %d rows", window, len(metrics))
		}
		m := metrics[0]
		if m.TPM != 60 || m.RequestCount != 0 || m.AvgUseTime != nil || m.SuccessRate != nil || m.LatencyBucketsV2 != nil || m.TTFTBuckets != nil {
			t.Fatalf("%s: TPM-only metric changed or retry duplicated: %#v", window, m)
		}
	}
}
