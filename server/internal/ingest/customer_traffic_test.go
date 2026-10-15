package ingest

import (
	"encoding/json"
	"testing"
	"time"

	"controltower/server/internal/agentgateway"
)

func TestCustomerTrafficSurvivesIngestRetryAndRollup(t *testing.T) {
	for _, tc := range []struct {
		name, counters   string
		requests, tokens int64
	}{
		{"legacy", `"tpm":30`, 0, 30},
		{"rpm", `"tpm":30,"request_count":2`, 2, 30},
		{"zero-token", `"tpm":0,"request_count":2`, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			service := NewService(store)
			var sparse agentgateway.AggregatedMetricPayload
			if err := json.Unmarshal([]byte(`{"bucket_time":"2026-09-14T10:00:00Z","window_seconds":60,"dimension_type":"instance_user_channel","dimension_key":"site:user:7:channel:3",`+tc.counters+`}`), &sparse); err != nil {
				t.Fatal(err)
			}
			// 重试和汇总使用当前重放窗口内的时间桶。
			sparse.BucketTime = time.Now().UTC().Truncate(5 * time.Minute)
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
				if m.TPM != 2*tc.tokens || m.RequestCount != 2*tc.requests || m.AvgUseTime != nil || m.SuccessRate != nil || m.ErrorRate != nil || m.StreamRate != nil || m.LatencyBucketsV2 != nil || m.TTFTBuckets != nil {
					t.Fatalf("%s: traffic metric changed or retry duplicated: %#v", window, m)
				}
			}
		})
	}
}
