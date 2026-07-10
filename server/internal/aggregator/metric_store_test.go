package aggregator

import (
	"testing"
	"time"
)

func TestMemoryMetricStoreUpserts1mAnd5mMetrics(t *testing.T) {
	store := NewMemoryMetricStore()
	bucket := time.Date(2026, 7, 2, 15, 0, 0, 0, time.UTC)
	first := Metric{
		InstanceID:    "inst-1",
		BucketTime:    bucket,
		DimensionType: "instance",
		DimensionKey:  "inst-1",
		RequestCount:  10,
	}
	second := first
	second.RequestCount = 20

	if err := store.Upsert1m([]Metric{first}); err != nil {
		t.Fatalf("upsert 1m first: %v", err)
	}
	if err := store.Upsert1m([]Metric{second}); err != nil {
		t.Fatalf("upsert 1m second: %v", err)
	}
	metrics := store.Recent1mMetrics()
	if len(metrics) != 1 {
		t.Fatalf("expected one upserted 1m metric, got %#v", metrics)
	}
	if metrics[0].RequestCount != 20 {
		t.Fatalf("expected replacement metric, got %#v", metrics[0])
	}

	if err := store.Upsert5m([]Metric{second}); err != nil {
		t.Fatalf("upsert 5m: %v", err)
	}
	if len(store.Recent5mMetrics()) != 1 {
		t.Fatalf("expected one 5m metric")
	}
}

func TestMemoryMetricStoreReturnsCopies(t *testing.T) {
	store := NewMemoryMetricStore()
	bucket := time.Date(2026, 7, 2, 15, 5, 0, 0, time.UTC)
	if err := store.Upsert1m([]Metric{{
		InstanceID:    "inst-1",
		BucketTime:    bucket,
		DimensionType: "instance",
		DimensionKey:  "inst-1",
		RequestCount:  10,
	}}); err != nil {
		t.Fatalf("upsert 1m: %v", err)
	}

	metrics := store.Recent1mMetrics()
	metrics[0].RequestCount = 999
	if store.Recent1mMetrics()[0].RequestCount == 999 {
		t.Fatalf("store should return copies")
	}
}

