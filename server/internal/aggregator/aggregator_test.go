package aggregator

import (
	"testing"
	"time"

	"controltower/server/internal/storage"
)

func TestAggregate1mComputesInstanceMetrics(t *testing.T) {
	bucket := time.Date(2026, 7, 2, 12, 34, 0, 0, time.UTC)
	cacheTokens := int64(20)
	metrics := Aggregate1m([]storage.LogEvent{
		{
			InstanceID:        "inst-1",
			CreatedAt:         bucket.Add(5 * time.Second),
			LogType:           "consume",
			PromptTokens:      100,
			CompletionTokens:  50,
			TotalTokens:       150,
			Quota:             500,
			UseTime:           1,
			IsStream:          true,
			CacheTokens:       &cacheTokens,
			CacheFieldPresent: true,
		},
		{
			InstanceID:       "inst-1",
			CreatedAt:        bucket.Add(20 * time.Second),
			LogType:          "error",
			PromptTokens:     10,
			CompletionTokens: 0,
			TotalTokens:      10,
			Quota:            0,
			UseTime:          5,
		},
	})

	metric, ok := findMetric(metrics, "instance", "inst-1")
	if !ok {
		t.Fatalf("missing instance metric: %#v", metrics)
	}
	if metric.RequestCount != 2 || metric.SuccessCount != 1 || metric.ErrorCount != 1 {
		t.Fatalf("unexpected counts: %#v", metric)
	}
	if metric.SuccessRate == nil || *metric.SuccessRate != 0.5 {
		t.Fatalf("unexpected success rate: %#v", metric.SuccessRate)
	}
	if metric.TPM != 160 {
		t.Fatalf("unexpected tpm: %d", metric.TPM)
	}
	if metric.AvgUseTime == nil || *metric.AvgUseTime != 3 {
		t.Fatalf("unexpected avg use time: %#v", metric.AvgUseTime)
	}
	if metric.P95UseTime == nil || *metric.P95UseTime != 4.8 {
		t.Fatalf("unexpected p95 use time: %#v", metric.P95UseTime)
	}
	if metric.StreamRate == nil || *metric.StreamRate != 0.5 {
		t.Fatalf("unexpected stream rate: %#v", metric.StreamRate)
	}
	if metric.CacheTokenRate == nil || *metric.CacheTokenRate != 0.2 {
		t.Fatalf("unexpected cache token rate: %#v", metric.CacheTokenRate)
	}
}

func TestAggregate1mBuildsDimensionMetrics(t *testing.T) {
	bucket := time.Date(2026, 7, 2, 12, 35, 0, 0, time.UTC)
	metrics := Aggregate1m([]storage.LogEvent{
		{
			InstanceID:  "inst-1",
			CreatedAt:   bucket,
			LogType:     "consume",
			UserID:      7,
			ChannelID:   18,
			ModelName:   "gpt-4o",
			TotalTokens: 10,
		},
	})

	for _, item := range []struct {
		dimensionType string
		dimensionKey  string
	}{
		{"instance", "inst-1"},
		{"instance_user", "inst-1:user:7"},
		{"instance_channel", "inst-1:channel:18"},
		{"instance_model", "inst-1:model:gpt-4o"},
		{"instance_user_model", "inst-1:user:7:model:gpt-4o"},
		{"instance_channel_model", "inst-1:channel:18:model:gpt-4o"},
		{"instance_model_user", "inst-1:model:gpt-4o:user:7"},
		{"instance_model_channel", "inst-1:model:gpt-4o:channel:18"},
	} {
		if _, ok := findMetric(metrics, item.dimensionType, item.dimensionKey); !ok {
			t.Fatalf("missing dimension metric %s/%s in %#v", item.dimensionType, item.dimensionKey, metrics)
		}
	}
}

func TestAggregate1mMissingCacheFieldsDoNotProduceZeroRate(t *testing.T) {
	bucket := time.Date(2026, 7, 2, 12, 36, 0, 0, time.UTC)
	metrics := Aggregate1m([]storage.LogEvent{
		{
			InstanceID:   "inst-1",
			CreatedAt:    bucket,
			LogType:      "consume",
			PromptTokens: 100,
			TotalTokens:  100,
		},
	})

	metric, ok := findMetric(metrics, "instance", "inst-1")
	if !ok {
		t.Fatalf("missing metric")
	}
	if metric.CacheTokenRate != nil {
		t.Fatalf("cache token rate should be nil when field is unavailable")
	}
}

func findMetric(metrics []Metric, dimensionType string, dimensionKey string) (Metric, bool) {
	for _, metric := range metrics {
		if metric.DimensionType == dimensionType && metric.DimensionKey == dimensionKey {
			return metric, true
		}
	}
	return Metric{}, false
}
