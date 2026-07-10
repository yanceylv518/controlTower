package aggregator

import (
	"testing"
	"time"

	"controltower/internal/latencyhist"
)

func TestRollup5mCombinesCountsAndRates(t *testing.T) {
	bucket := time.Date(2026, 7, 2, 12, 40, 0, 0, time.UTC)
	avg1 := 2.0
	avg2 := 6.0

	metrics := Rollup5m([]Metric{
		{
			InstanceID:       "inst-1",
			BucketTime:       bucket.Add(1 * time.Minute),
			DimensionType:    "instance",
			DimensionKey:     "inst-1",
			RequestCount:     10,
			SuccessCount:     9,
			ErrorCount:       1,
			TPM:              100,
			PromptTokens:     70,
			CompletionTokens: 30,
			Quota:            200,
			AvgUseTime:       &avg1,
			LatencyBuckets:   latencyBuckets(9, 1, 1, 30),
		},
		{
			InstanceID:       "inst-1",
			BucketTime:       bucket.Add(4 * time.Minute),
			DimensionType:    "instance",
			DimensionKey:     "inst-1",
			RequestCount:     30,
			SuccessCount:     21,
			ErrorCount:       9,
			TPM:              300,
			PromptTokens:     200,
			CompletionTokens: 100,
			Quota:            600,
			AvgUseTime:       &avg2,
			LatencyBuckets:   latencyBuckets(29, 1, 2, 60),
		},
	})

	if len(metrics) != 1 {
		t.Fatalf("expected one rolled metric, got %#v", metrics)
	}
	metric := metrics[0]
	if !metric.BucketTime.Equal(bucket) {
		t.Fatalf("unexpected bucket: %s", metric.BucketTime)
	}
	if metric.RequestCount != 40 || metric.SuccessCount != 30 || metric.ErrorCount != 10 {
		t.Fatalf("unexpected counts: %#v", metric)
	}
	if metric.SuccessRate == nil || *metric.SuccessRate != 0.75 {
		t.Fatalf("unexpected success rate: %#v", metric.SuccessRate)
	}
	if metric.AvgUseTime == nil || *metric.AvgUseTime != 5.0 {
		t.Fatalf("unexpected weighted avg: %#v", metric.AvgUseTime)
	}
	if metric.P95UseTime == nil || *metric.P95UseTime != 2.0 {
		t.Fatalf("rollup should derive p95 from merged histogram, got %#v", metric.P95UseTime)
	}
}

func latencyBuckets(fastCount, slowCount int64, fastSeconds, slowSeconds float64) latencyhist.Buckets {
	var buckets latencyhist.Buckets
	buckets[latencyhist.Index(fastSeconds)] = fastCount
	buckets[latencyhist.Index(slowSeconds)] = slowCount
	return buckets
}

func TestRollup5mKeepsSeparateDimensions(t *testing.T) {
	bucket := time.Date(2026, 7, 2, 12, 40, 0, 0, time.UTC)
	metrics := Rollup5m([]Metric{
		{
			InstanceID:    "inst-1",
			BucketTime:    bucket,
			DimensionType: "instance",
			DimensionKey:  "inst-1",
			RequestCount:  10,
		},
		{
			InstanceID:    "inst-1",
			BucketTime:    bucket,
			DimensionType: "instance_model",
			DimensionKey:  "inst-1:model:gpt-4o",
			RequestCount:  20,
		},
	})

	if len(metrics) != 2 {
		t.Fatalf("expected separate dimension metrics, got %#v", metrics)
	}
}
