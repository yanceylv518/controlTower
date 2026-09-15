package ingest

import (
	"controltower/internal/speedstats"
	"controltower/server/internal/agentgateway"
	"testing"
	"time"
)

func TestSpeedEvidenceMergeLegacyAndDedup(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	now := time.Now().UTC().Truncate(time.Minute)
	count := int64(2)
	stats := speedstats.New()
	stats.Buckets[2] = 1
	stats.RetryCount = 1
	report := func(id string, s *speedstats.Stats) {
		t.Helper()
		err := service.SaveReport(agentgateway.AgentReportRequest{InstanceID: "i", AgentID: "a", ReportedAt: now, MetricBatchID: id, AggregatedMetrics: []agentgateway.AggregatedMetricPayload{{BucketTime: now, WindowSeconds: 60, DimensionType: "instance_channel", DimensionKey: "i:channel:7", RequestCount: 2, TTFTCount: &count, SpeedTTFT: s}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	report("first", stats)
	report("first", stats)
	report("second", stats)
	report("legacy", nil)
	items, err := store.Recent1mMetrics()
	if err != nil || len(items) != 1 {
		t.Fatalf("%+v %v", items, err)
	}
	m := items[0]
	if m.RequestCount != 6 || *m.TTFTCount != 6 || m.SpeedTTFT.Samples() != 2 || m.SpeedTTFT.RetryCount != 2 {
		t.Fatalf("bad merge %+v %+v", m, m.SpeedTTFT)
	}
	if stats.Samples() != 1 {
		t.Fatal("merge mutated incoming batch")
	}
}

func TestInvalidSpeedEvidenceDoesNotBlockMonitoring(t *testing.T) {
	total := int64(1)
	for _, s := range []*speedstats.Stats{nil, {Buckets: []int64{1}}, {Buckets: make([]int64, 15), RetryCount: -1}, {Buckets: make([]int64, 15), RetryCount: 2}} {
		p := agentgateway.AggregatedMetricPayload{BucketTime: time.Now(), DimensionKey: "i:channel:7", DimensionType: "instance_channel", TTFTCount: &total, SpeedTTFT: s}
		if validSpeedTTFT(p) != nil {
			t.Fatal("invalid evidence accepted")
		}
		m := toAggregatorMetrics("i", []agentgateway.AggregatedMetricPayload{p})
		if len(m) != 1 || *m[0].TTFTCount != 1 {
			t.Fatal("public evidence lost")
		}
	}
}
