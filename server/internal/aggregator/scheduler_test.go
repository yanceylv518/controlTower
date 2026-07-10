package aggregator

import (
	"testing"
	"time"

	"controltower/server/internal/storage"
)

func TestSchedulerRunOnceStores1mAnd5mMetrics(t *testing.T) {
	store := NewMemoryMetricStore()
	scheduler := NewScheduler(store)
	baseTime := time.Date(2026, 7, 2, 13, 24, 30, 0, time.UTC)
	cacheTokens := int64(25)

	result, err := scheduler.RunOnce([]storage.LogEvent{
		{
			InstanceID:        "instance-a",
			SourceLogID:       101,
			CreatedAt:         baseTime,
			LogType:           "consume",
			UserID:            10,
			ChannelID:         20,
			ModelName:         "gpt-test",
			PromptTokens:      100,
			CompletionTokens:  50,
			TotalTokens:       150,
			Quota:             300,
			UseTime:           1.2,
			IsStream:          true,
			CacheTokens:       &cacheTokens,
			CacheFieldPresent: true,
		},
		{
			InstanceID:       "instance-a",
			SourceLogID:      102,
			CreatedAt:        baseTime.Add(20 * time.Second),
			LogType:          "error",
			UserID:           10,
			ChannelID:        20,
			ModelName:        "gpt-test",
			PromptTokens:     80,
			CompletionTokens: 0,
			TotalTokens:      80,
			Quota:            160,
			UseTime:          2.8,
		},
	})
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if result.Events != 2 {
		t.Fatalf("Events = %d, want 2", result.Events)
	}
	if result.Metrics1m == 0 {
		t.Fatal("Metrics1m = 0, want stored 1m metrics")
	}
	if result.Metrics5m == 0 {
		t.Fatal("Metrics5m = 0, want stored 5m metrics")
	}

	metrics1m := store.Recent1mMetrics()
	if len(metrics1m) != result.Metrics1m {
		t.Fatalf("stored 1m metrics = %d, want result count %d", len(metrics1m), result.Metrics1m)
	}
	instanceMetric, ok := findMetric(metrics1m, "instance", "instance-a")
	if !ok {
		t.Fatalf("missing instance 1m metric: %#v", metrics1m)
	}
	if instanceMetric.BucketTime != baseTime.Truncate(time.Minute) {
		t.Fatalf("1m bucket = %s, want %s", instanceMetric.BucketTime, baseTime.Truncate(time.Minute))
	}
	if instanceMetric.RequestCount != 2 {
		t.Fatalf("1m request count = %d, want 2", instanceMetric.RequestCount)
	}
	if instanceMetric.SuccessCount != 1 {
		t.Fatalf("1m success count = %d, want 1", instanceMetric.SuccessCount)
	}
	if instanceMetric.ErrorCount != 1 {
		t.Fatalf("1m error count = %d, want 1", instanceMetric.ErrorCount)
	}

	metrics5m := store.Recent5mMetrics()
	if len(metrics5m) != result.Metrics5m {
		t.Fatalf("stored 5m metrics = %d, want result count %d", len(metrics5m), result.Metrics5m)
	}
	rollupMetric, ok := findMetric(metrics5m, "instance", "instance-a")
	if !ok {
		t.Fatalf("missing instance 5m metric: %#v", metrics5m)
	}
	if rollupMetric.BucketTime != time.Date(2026, 7, 2, 13, 20, 0, 0, time.UTC) {
		t.Fatalf("5m bucket = %s, want 2026-07-02 13:20:00 UTC", rollupMetric.BucketTime)
	}
	if rollupMetric.RequestCount != 2 {
		t.Fatalf("5m request count = %d, want 2", rollupMetric.RequestCount)
	}
}

func TestSchedulerRunOnceSkipsEmptyEvents(t *testing.T) {
	store := NewMemoryMetricStore()
	scheduler := NewScheduler(store)

	result, err := scheduler.RunOnce(nil)
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if result.Events != 0 || result.Metrics1m != 0 || result.Metrics5m != 0 {
		t.Fatalf("RunOnce result = %+v, want all counts zero", result)
	}
	if len(store.Recent1mMetrics()) != 0 {
		t.Fatal("stored 1m metrics for empty events, want none")
	}
	if len(store.Recent5mMetrics()) != 0 {
		t.Fatal("stored 5m metrics for empty events, want none")
	}
}

func TestSchedulerRunOnceOverwritesExistingBuckets(t *testing.T) {
	store := NewMemoryMetricStore()
	scheduler := NewScheduler(store)
	baseTime := time.Date(2026, 7, 2, 13, 24, 30, 0, time.UTC)
	events := []storage.LogEvent{
		{
			InstanceID:       "instance-a",
			SourceLogID:      201,
			CreatedAt:        baseTime,
			LogType:          "consume",
			ModelName:        "gpt-test",
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
			Quota:            30,
			UseTime:          0.9,
		},
	}

	first, err := scheduler.RunOnce(events)
	if err != nil {
		t.Fatalf("first RunOnce returned error: %v", err)
	}
	second, err := scheduler.RunOnce(events)
	if err != nil {
		t.Fatalf("second RunOnce returned error: %v", err)
	}
	if second.Metrics1m != first.Metrics1m || second.Metrics5m != first.Metrics5m {
		t.Fatalf("second result = %+v, want same metric counts as first %+v", second, first)
	}
	if len(store.Recent1mMetrics()) != first.Metrics1m {
		t.Fatalf("stored 1m metrics after duplicate run = %d, want %d", len(store.Recent1mMetrics()), first.Metrics1m)
	}
	if len(store.Recent5mMetrics()) != first.Metrics5m {
		t.Fatalf("stored 5m metrics after duplicate run = %d, want %d", len(store.Recent5mMetrics()), first.Metrics5m)
	}
}
