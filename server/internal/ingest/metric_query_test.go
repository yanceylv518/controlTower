package ingest

import (
	"testing"
	"time"

	"controltower/server/internal/aggregator"
)

func TestMemoryMetricHistoryFiltersSortsAndLatestKeepsQuietDimensions(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now().UTC().Truncate(time.Minute)
	metrics := []aggregator.Metric{
		{InstanceID: "inst", BucketTime: now.Add(-2 * time.Minute), DimensionType: "instance_user", DimensionKey: "inst:user:quiet", RequestCount: 1},
		{InstanceID: "inst", BucketTime: now.Add(-time.Minute), DimensionType: "instance_user", DimensionKey: "inst:user:active", RequestCount: 2},
		{InstanceID: "inst", BucketTime: now, DimensionType: "instance_user", DimensionKey: "inst:user:active", RequestCount: 3},
	}
	if err := store.Upsert1m(metrics); err != nil {
		t.Fatal(err)
	}
	latest, err := store.Latest1mMetrics("instance_user")
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 2 {
		t.Fatalf("latest len=%d, want both dimensions", len(latest))
	}
	history, err := store.QueryMetricHistory("1m", "instance_user", "inst:user:active", now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || !history[0].BucketTime.Before(history[1].BucketTime) {
		t.Fatalf("history not filtered ascending: %#v", history)
	}
}

func TestMemoryUsageSummaryFiltersTimeAndAggregates(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now().UTC()
	_ = store.Upsert1m([]aggregator.Metric{{BucketTime: now, DimensionType: "instance_user", DimensionKey: "inst:user:7", RequestCount: 2, PromptTokens: 3, CompletionTokens: 4, Quota: 5}, {BucketTime: now.Add(-48 * time.Hour), DimensionType: "instance_user", DimensionKey: "inst:user:7", RequestCount: 100, Quota: 100}})
	rows, err := store.UsageSummary(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RequestCount != 2 || rows[0].PromptTokens+rows[0].CompletionTokens != 7 || rows[0].Quota != 5 {
		t.Fatalf("usage=%#v", rows)
	}
}
