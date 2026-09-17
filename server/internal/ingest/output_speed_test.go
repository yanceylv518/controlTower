package ingest

import (
	"controltower/internal/outputstats"
	"controltower/server/internal/agentgateway"
	"encoding/json"
	"testing"
	"time"
)

func TestOutputEvidenceBatchDedupAndRollup(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	now := time.Now().UTC().Truncate(5 * time.Minute)
	stats := &outputstats.Stats{}
	stats.Add(600, 15, 1, true)
	stats.Add(400, 25, 2, true)
	for _, id := range []string{"a", "a", "b", "legacy"} {
		s := stats
		at := now
		if id == "b" {
			at = at.Add(time.Minute)
		}
		if id == "legacy" {
			s = nil
		}
		err := service.SaveReport(agentgateway.AgentReportRequest{InstanceID: "i", AgentID: "a", ReportedAt: now, MetricBatchID: id, AggregatedMetrics: []agentgateway.AggregatedMetricPayload{{BucketTime: at, WindowSeconds: 60, DimensionType: "instance_channel", DimensionKey: "i:channel:7", RequestCount: 2, OutputSpeed: s, OTPSOutputTokens: 10000, OTPSDurationSecs: 1}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.Recent5mMetrics()
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	m := items[0]
	if m.RequestCount != 6 || m.OutputSpeed.Samples != 4 || m.OutputSpeed.Tokens != 2000 || m.OutputSpeed.Seconds != 80 || *m.OutputSpeed.Rate() != 25 || m.OutputSpeed.DirectSamples != 2 {
		t.Fatalf("%+v", m)
	}
	if stats.Tokens != 1000 {
		t.Fatal("input mutated")
	}
}

func TestMalformedOutputEvidenceDoesNotBlockPublicReport(t *testing.T) {
	for _, wire := range []string{`{"request_count":1,"output_speed":"bad"}`, `{"request_count":1,"output_speed":{"tokens":10,"seconds":0,"samples":1}}`} {
		var p agentgateway.AggregatedMetricPayload
		if err := json.Unmarshal([]byte(wire), &p); err != nil {
			t.Fatal(err)
		}
		p.BucketTime = time.Now()
		p.DimensionType = "instance_channel"
		p.DimensionKey = "i:channel:7"
		got := toAggregatorMetrics("i", []agentgateway.AggregatedMetricPayload{p})
		if len(got) != 1 || got[0].RequestCount != 1 || got[0].OutputSpeed != nil {
			t.Fatalf("%+v", got)
		}
	}
}
