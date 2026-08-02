package billing

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeRollupSource struct {
	calls    int
	failSite string
}

func (f *fakeRollupSource) Logs(_ context.Context, site string, start, _ time.Time) ([]LogRecord, error) {
	f.calls++
	if site == f.failSite {
		return nil, errors.New("offline")
	}
	if start.Hour() != 3 {
		return nil, nil
	}
	return []LogRecord{
		{UserID: 1, Username: "alice", ModelName: "model-a", GroupName: "vip", PromptTokens: 127999, CompletionTokens: 10, Quota: 20},
		{UserID: 1, Username: "alice", ModelName: "model-a", GroupName: "vip", PromptTokens: 128000, CompletionTokens: 20, CacheTokens: 5, Quota: 30},
	}, nil
}
func (f *fakeRollupSource) RatioSnapshot(context.Context, string) (string, error) {
	return `{"QuotaPerUnit":500000}`, nil
}
func (f *fakeRollupSource) Balances(context.Context, string) (map[int64]int64, error) {
	return map[int64]int64{1: 99}, nil
}

type fakeRollupStore struct {
	prices       []PriceRecord
	replacements [][]DailyRow
	ratios       int
	balances     int
}

func (f *fakeRollupStore) ListBillingPrices(context.Context, string) ([]PriceRecord, error) {
	return f.prices, nil
}
func (f *fakeRollupStore) ReplaceBillingDay(_ context.Context, _ string, _ time.Time, rows []DailyRow) error {
	copied := append([]DailyRow(nil), rows...)
	f.replacements = append(f.replacements, copied)
	return nil
}
func (f *fakeRollupStore) PutBillingRatioSnapshot(context.Context, string, time.Time, string) error {
	f.ratios++
	return nil
}
func (f *fakeRollupStore) PutBillingBalanceSnapshots(context.Context, string, time.Time, map[int64]int64) error {
	f.balances++
	return nil
}

func TestRollupDayUses24SegmentsAndClassifiesBeforeAggregation(t *testing.T) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	source := &fakeRollupSource{}
	store := &fakeRollupStore{prices: []PriceRecord{
		{InstanceID: "cn", ModelName: "model-a", Price: Price{EffectiveFrom: day, TierFrom: 0, Input: "1"}},
		{InstanceID: "cn", ModelName: "model-a", Price: Price{EffectiveFrom: day, TierFrom: 128000, Input: "2"}},
	}}
	var pauses []time.Duration
	service := RollupService{Source: source, Store: store, Sleep: func(_ context.Context, d time.Duration) error { pauses = append(pauses, d); return nil }, Now: func() time.Time { return day.Add(26 * time.Hour) }}
	result, err := service.RollupDay(context.Background(), "cn", day)
	if err != nil {
		t.Fatal(err)
	}
	if source.calls != 24 {
		t.Fatalf("hour queries = %d, want 24", source.calls)
	}
	if len(pauses) != 23 {
		t.Fatalf("segment pauses = %d, want 23", len(pauses))
	}
	if result.Rows != 2 || len(store.replacements) != 1 || len(store.replacements[0]) != 2 {
		t.Fatalf("unexpected rollup result: %#v %#v", result, store.replacements)
	}
	if store.replacements[0][0].TierFrom != 0 || store.replacements[0][1].TierFrom != 128000 {
		t.Fatalf("tiers = %d,%d", store.replacements[0][0].TierFrom, store.replacements[0][1].TierFrom)
	}
	if store.ratios != 1 || store.balances != 1 {
		t.Fatalf("snapshots ratio=%d balance=%d", store.ratios, store.balances)
	}
}

func TestRollupDayIsIdempotentByReplacingTheWholeDay(t *testing.T) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	source, store := &fakeRollupSource{}, &fakeRollupStore{}
	service := RollupService{Source: source, Store: store, SegmentPause: time.Nanosecond, Sleep: func(context.Context, time.Duration) error { return nil }}
	if _, err := service.RollupDay(context.Background(), "cn", day); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RollupDay(context.Background(), "cn", day); err != nil {
		t.Fatal(err)
	}
	if len(store.replacements) != 2 {
		t.Fatalf("replacements = %d", len(store.replacements))
	}
	for i := range store.replacements[0] {
		if store.replacements[0][i].RequestCount != store.replacements[1][i].RequestCount {
			t.Fatal("rerun doubled daily values")
		}
	}
}

func TestRollupSitesDoesNotStopAfterOneSiteFails(t *testing.T) {
	source := &fakeRollupSource{failSite: "bad"}
	store := &fakeRollupStore{}
	service := RollupService{Source: source, Store: store, SegmentPause: time.Nanosecond, DayPause: time.Nanosecond, Sleep: func(context.Context, time.Duration) error { return nil }}
	errs := service.RollupSites(context.Background(), []string{"bad", "good"}, time.Now())
	if errs["bad"] == nil {
		t.Fatal("bad site error missing")
	}
	if errs["good"] != nil {
		t.Fatalf("good site failed: %v", errs["good"])
	}
	if len(store.replacements) != 1 {
		t.Fatalf("good site was not persisted: %d", len(store.replacements))
	}
}
