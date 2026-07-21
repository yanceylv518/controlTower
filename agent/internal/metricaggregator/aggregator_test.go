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

	metrics := Aggregate("inst-1", events, 512)
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

func TestAggregateExactQuantilesCacheBoundaryAndStreamTTFT(t *testing.T) {
	base := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	hit, miss := int64(8), int64(0)
	frt100, frt300 := int64(100), int64(300)
	events := []logcollector.Event{
		{CreatedAt: base, LogType: "consume", PromptTokens: 512, UseTime: 1, IsStream: true, CacheTokens: &hit, CacheFieldPresent: true, FirstResponseMs: &frt100},
		{CreatedAt: base, LogType: "consume", PromptTokens: 513, UseTime: 2, IsStream: true, CacheTokens: &hit, CacheFieldPresent: true, FirstResponseMs: &frt300},
		{CreatedAt: base, LogType: "consume", PromptTokens: 600, UseTime: 9, CacheTokens: &miss, CacheFieldPresent: true, FirstResponseMs: &frt300},
	}
	metrics := Aggregate("inst-1", events, 512)
	metric := metrics[0]
	if metric.P50UseTime == nil || *metric.P50UseTime != 2 || metric.P95UseTime == nil || *metric.P95UseTime != 9 || metric.P99UseTime == nil || *metric.P99UseTime != 9 {
		t.Fatalf("unexpected exact quantiles: %#v", metric)
	}
	if metric.BigInputCount == nil || *metric.BigInputCount != 2 || metric.BigInputCacheHits == nil || *metric.BigInputCacheHits != 1 {
		t.Fatalf("unexpected cache boundary metrics: %#v", metric)
	}
	if metric.TTFTCount == nil || *metric.TTFTCount != 2 || metric.TTFTSumMS == nil || *metric.TTFTSumMS != 400 || metric.TTFTP95MS == nil || *metric.TTFTP95MS != 300 {
		t.Fatalf("unexpected ttft metrics: %#v", metric)
	}
}

func TestAggregateOTPSUsesOnlyValidSuccessfulStreamGenerationTime(t *testing.T) {
	base := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	firstResponse := int64(1000)
	metrics := Aggregate("inst", []logcollector.Event{
		{CreatedAt: base, LogType: "consume", UseTime: 5, IsStream: true, FirstResponseMs: &firstResponse, CompletionTokens: 80},
		{CreatedAt: base, LogType: "error", UseTime: 5, IsStream: true, FirstResponseMs: &firstResponse, CompletionTokens: 100},
		{CreatedAt: base, LogType: "consume", UseTime: 5, CompletionTokens: 100},
	}, 512)
	if len(metrics) != 1 || metrics[0].OTPSOutputTokens != 80 || metrics[0].OTPSDurationSecs != 4 {
		t.Fatalf("unexpected OTPS accumulators: %#v", metrics)
	}
}

func TestAggregateCapsRawQuantileValues(t *testing.T) {
	base := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	events := make([]logcollector.Event, maxRawValuesPerBucket+1)
	for i := range events {
		events[i] = logcollector.Event{CreatedAt: base, LogType: "consume", UseTime: 1}
	}
	events[len(events)-1].UseTime = 120
	metric := Aggregate("inst-1", events, 512)[0]
	if metric.P99UseTime == nil || *metric.P99UseTime != 1 {
		t.Fatalf("raw cap should exclude overflow value: %#v", metric.P99UseTime)
	}
}
