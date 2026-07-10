package metricaggregator

import (
	"testing"
	"time"

	"controltower/agent/internal/logcollector"
)

func TestAggregateBuildsCoreDimensions(t *testing.T) {
	base := time.Date(2026, 7, 8, 10, 0, 10, 0, time.UTC)
	cacheTokens := int64(8)
	events := []logcollector.Event{
		{CreatedAt: base, LogType: "consume", UserID: 7, ChannelID: 18, ModelName: "gpt-4o", PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30, Quota: 60, UseTime: 1.0, IsStream: true, CacheTokens: &cacheTokens, CacheFieldPresent: true},
		{CreatedAt: base.Add(10 * time.Second), LogType: "error", UserID: 7, ChannelID: 18, ModelName: "gpt-4o", PromptTokens: 5, TotalTokens: 5, UseTime: 3.0},
	}

	metrics := Aggregate("inst-1", events)
	byType := map[string]int{}
	for _, metric := range metrics {
		byType[metric.DimensionType]++
		if metric.WindowSeconds != 60 {
			t.Fatalf("unexpected window: %d", metric.WindowSeconds)
		}
	}
	for _, dimensionType := range []string{"instance", "instance_user", "instance_channel", "instance_model", "instance_user_model", "instance_model_user", "instance_channel_model", "instance_model_channel"} {
		if byType[dimensionType] != 1 {
			t.Fatalf("dimension %s count = %d, metrics=%#v", dimensionType, byType[dimensionType], metrics)
		}
	}

	var instanceMetricFound bool
	for _, metric := range metrics {
		if metric.DimensionType != "instance" {
			continue
		}
		instanceMetricFound = true
		if metric.RequestCount != 2 || metric.SuccessCount != 1 || metric.ErrorCount != 1 {
			t.Fatalf("unexpected counts: %#v", metric)
		}
		if metric.PromptTokens != 15 || metric.CompletionTokens != 20 || metric.TPM != 35 || metric.Quota != 60 {
			t.Fatalf("unexpected token totals: %#v", metric)
		}
		if metric.SuccessRate == nil || *metric.SuccessRate != 0.5 || metric.ErrorRate == nil || *metric.ErrorRate != 0.5 {
			t.Fatalf("unexpected rates: %#v", metric)
		}
		if metric.AvgUseTime == nil || *metric.AvgUseTime != 2.0 || metric.P95UseTime == nil || *metric.P95UseTime != 3.0 {
			t.Fatalf("unexpected latency: %#v", metric)
		}
	}
	if !instanceMetricFound {
		t.Fatalf("instance metric not found: %#v", metrics)
	}
}
