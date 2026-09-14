package aggregator

import (
	"reflect"
	"testing"
	"time"

	"controltower/server/internal/storage"
)

func TestCustomerChannelRawAndRollup(t *testing.T) {
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	metrics := Aggregate1m([]storage.LogEvent{
		{InstanceID: "site", CreatedAt: now, UserID: 7, ChannelID: 3, TotalTokens: 30},
		{InstanceID: "site", CreatedAt: now.Add(time.Minute), UserID: 7, ChannelID: 3, TotalTokens: 40},
		{InstanceID: "site", CreatedAt: now, UserID: 8, ChannelID: 3, TotalTokens: 90},
		{InstanceID: "other", CreatedAt: now, UserID: 7, ChannelID: 3, TotalTokens: 60},
	})
	var combined Metric
	for _, m := range metrics {
		if m.DimensionType == "instance_user_channel" && m.DimensionKey == "site:user:7:channel:3" {
			combined = MergeMetric(combined, m)
		}
	}
	if combined.TPM != 70 || combined.RequestCount != 0 {
		t.Fatalf("wrong customer total: %#v", combined)
	}
	rolled, ok := findMetric(Rollup5m(metrics), "instance_user_channel", "site:user:7:channel:3")
	if !ok || rolled.TPM != 70 || rolled.RequestCount != 0 {
		t.Fatalf("wrong 5m customer/channel rollup: %#v", rolled)
	}
	onlyTokens := Metric{InstanceID: rolled.InstanceID, BucketTime: rolled.BucketTime, DimensionType: rolled.DimensionType, DimensionKey: rolled.DimensionKey, TPM: 70}
	if !reflect.DeepEqual(rolled, onlyTokens) {
		t.Fatalf("rollup synthesized non-TPM statistics: %#v", rolled)
	}
	if !reflect.DeepEqual(combined, onlyTokens) {
		t.Fatalf("merge synthesized non-TPM statistics: %#v", combined)
	}
}

func TestCustomerChannelRawPreservesExistingMetrics(t *testing.T) {
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	cache := int64(700)
	events := []storage.LogEvent{
		{InstanceID: "site", CreatedAt: now, UserID: 7, ChannelID: 3, ModelName: "a", LogType: "consume", TotalTokens: 1500, PromptTokens: 1000, CompletionTokens: 500, UseTime: 2, IsStream: true, CacheTokens: &cache, CacheFieldPresent: true},
		{InstanceID: "site", CreatedAt: now, UserID: 7, ChannelID: 3, ModelName: "a", LogType: "error", UseTime: 4},
		{InstanceID: "site", CreatedAt: now.Add(time.Minute), UserID: 7, ChannelID: 8, ModelName: "b", LogType: "consume", TotalTokens: 500, PromptTokens: 500, UseTime: 1},
		{InstanceID: "other", CreatedAt: now, UserID: 7, ChannelID: 3, TotalTokens: 30},
	}
	type key struct {
		instance, dimension, id string
		bucket                  time.Time
	}
	want := map[key]*accumulator{}
	for _, event := range events {
		for _, dim := range dimensionsFor(event) {
			k := key{event.InstanceID, dim.dimensionType, dim.dimensionKey, event.CreatedAt.Truncate(time.Minute)}
			if want[k] == nil {
				want[k] = &accumulator{metric: Metric{InstanceID: k.instance, DimensionType: k.dimension, DimensionKey: k.id, BucketTime: k.bucket}}
			}
			want[k].add(event)
		}
	}
	for _, m := range Aggregate1m(events) {
		if m.DimensionType == "instance_user_channel" {
			onlyTokens := Metric{InstanceID: m.InstanceID, DimensionType: m.DimensionType, DimensionKey: m.DimensionKey, BucketTime: m.BucketTime, TPM: m.TPM}
			if !reflect.DeepEqual(m, onlyTokens) {
				t.Fatal("raw aggregation emitted non-TPM data")
			}
			continue
		}
		k := key{m.InstanceID, m.DimensionType, m.DimensionKey, m.BucketTime}
		if want[k] == nil || !reflect.DeepEqual(m, want[k].finalize()) {
			t.Fatalf("existing metric changed: %s", m.DimensionKey)
		}
		delete(want, k)
	}
	if len(want) != 0 {
		t.Fatal("existing metrics missing")
	}
}
