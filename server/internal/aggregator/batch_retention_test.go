package aggregator

import (
	"reflect"
	"testing"
	"time"
)

func TestMetricBatchReplayBoundaryAndMixedBuckets(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	cutoff := now.Add(-MetricBatchReplayWindow)
	input := []Metric{{BucketTime: cutoff.Add(-time.Microsecond), RequestCount: 100}, {BucketTime: cutoff, RequestCount: 2}, {BucketTime: now, RequestCount: 3}, {BucketTime: now.Add(48 * time.Hour), RequestCount: 4}}
	before := append([]Metric(nil), input...)
	kept, expiry := RetainMetricBatch(input, now)
	if len(kept) != 3 || kept[0].RequestCount != 2 || !reflect.DeepEqual(input, before) {
		t.Fatalf("boundary, mixed batch or input changed: %+v", kept)
	}
	if !expiry.Equal(input[3].BucketTime.Add(MetricBatchRetention)) {
		t.Fatalf("future bucket must extend expiry: %s", expiry)
	}
	// Once this key can be cleaned, every accepted bucket is already too old.
	replay, _ := RetainMetricBatch(input, expiry.Add(time.Microsecond))
	if len(replay) != 0 {
		t.Fatal("expired key would permit counting its metrics twice")
	}
}

func TestMetricBatchEmptyAndLegacyTimestamps(t *testing.T) {
	for _, metrics := range [][]Metric{nil, {{BucketTime: time.Time{}}}} {
		kept, _ := RetainMetricBatch(metrics, time.Now().UTC())
		if len(kept) != 0 {
			t.Fatal("empty/undated metric must not create a ledger")
		}
	}
}
