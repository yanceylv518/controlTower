package billing

import (
	"testing"
	"time"
)

func TestParseRatioSnapshotAndFallbackPrice(t *testing.T) {
	raw := `{"ModelRatio":"{\"gpt-test\":2}","CompletionRatio":"{\"gpt-test\":3}","CacheRatio":"{\"gpt-test\":0.5}","GroupRatio":"{\"vip\":1.2}","QuotaPerUnit":500000}`
	snapshot, err := ParseRatioSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	price, ratio, err := FallbackPrice(snapshot, "gpt-test", "vip")
	if err != nil {
		t.Fatal(err)
	}
	if price.Input != "4.000000000000" || price.Output != "12.000000000000" || price.Cache != "2.000000000000" || price.CacheWrite != "0.000000000000" || ratio != "1.2" {
		t.Fatalf("unexpected fallback: %#v ratio=%s", price, ratio)
	}
}

func TestFallbackPriceDoesNotDefaultCacheWritePrice(t *testing.T) {
	snapshot, err := ParseRatioSnapshot(`{"ModelRatio":"{\"gpt-test\":2}","QuotaPerUnit":500000}`)
	if err != nil {
		t.Fatal(err)
	}
	price, _, err := FallbackPrice(snapshot, "gpt-test", "")
	if err != nil || price.CacheWrite != "0.000000000000" {
		t.Fatalf("unexpected unconfigured cache write price: %#v err=%v", price, err)
	}
}

func TestFallbackPriceUsesConfiguredCreateCacheRatio(t *testing.T) {
	snapshot, err := ParseRatioSnapshot(`{"ModelRatio":"{\"gpt-test\":2}","CreateCacheRatio":"{\"gpt-test\":1.4}","QuotaPerUnit":500000}`)
	if err != nil {
		t.Fatal(err)
	}
	price, _, err := FallbackPrice(snapshot, "gpt-test", "")
	if err != nil || price.CacheWrite != "5.600000000000" {
		t.Fatalf("unexpected configured cache write price: %#v err=%v", price, err)
	}
}

func TestBuildDetailsZerosHistoricalCacheWriteWithoutExplicitRatio(t *testing.T) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	rows := []AggregateRow{{ModelName: "priced", Day: day, CacheWriteTokens: 1_000_000}}
	prices := []PriceRecord{{ModelName: "priced", Price: Price{EffectiveFrom: day, Input: "10", Output: "20", Cache: "1", CacheWrite: "12.5"}}}
	snapshots := map[string]string{"2026-08-01": `{"ModelRatio":"{\"priced\":2}","QuotaPerUnit":500000}`}
	items := BuildDetails(rows, prices, nil, snapshots)
	if len(items) != 1 || items[0].CacheWritePrice != "0.000000" || items[0].Amount != "0.000000" {
		t.Fatalf("unexpected cache write fallback: %#v", items)
	}
}

func TestBuildDetailsKeepsCacheWriteWithExplicitRatio(t *testing.T) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	rows := []AggregateRow{{ModelName: "priced", Day: day, CacheWriteTokens: 1_000_000}}
	prices := []PriceRecord{{ModelName: "priced", Price: Price{EffectiveFrom: day, Input: "10", Output: "20", Cache: "1", CacheWrite: "12.5"}}}
	snapshots := map[string]string{"2026-08-01": `{"ModelRatio":"{\"priced\":2}","CreateCacheRatio":"{\"priced\":1.25}","QuotaPerUnit":500000}`}
	items := BuildDetails(rows, prices, nil, snapshots)
	if len(items) != 1 || items[0].CacheWritePrice != "12.500000" || items[0].Amount != "12.500000" {
		t.Fatalf("unexpected configured cache write: %#v", items)
	}
}

func TestBuildSummaryUsesCTThenActualQuota(t *testing.T) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	rows := []AggregateRow{
		{UserID: 7, Username: "alice", ModelName: "priced", GroupName: "vip", Day: day, PromptTokens: 1_000_000},
		{UserID: 7, Username: "alice", ModelName: "fallback", GroupName: "vip", Day: day, CompletionTokens: 1_000_000, Quota: 7_200_000},
		{UserID: 7, Username: "alice", ModelName: "missing", GroupName: "vip", Day: day, PromptTokens: 100, Quota: 500_000},
	}
	prices := []PriceRecord{{ModelName: "priced", Price: Price{EffectiveFrom: day, TierFrom: 0, Input: "2", Output: "4", Cache: "1"}}}
	ratios := []GroupRatio{{GroupName: "vip", Ratio: "1.5"}}
	snapshots := map[string]string{"2026-08-01": `{"ModelRatio":"{\"fallback\":2}","CompletionRatio":"{\"fallback\":3}","GroupRatio":"{\"vip\":1.2}","QuotaPerUnit":500000}`}
	items, total := BuildSummary(rows, prices, ratios, snapshots, map[int64]int64{7: 99})
	if len(items) != 1 || items[0].Amount != "18.400000" || total.Amount != "18.400000" {
		t.Fatalf("unexpected summary: %#v total=%#v", items, total)
	}
	if len(items[0].UnpricedModels) != 0 {
		t.Fatalf("unexpected unpriced models: %#v", items[0].UnpricedModels)
	}
	if len(items[0].PriceSources) != 2 || items[0].PriceSources[0] != "ct" || items[0].PriceSources[1] != "newapi" {
		t.Fatalf("unexpected sources: %#v", items[0].PriceSources)
	}
}

func TestBuildDetailsShowsCTAndQuotaSources(t *testing.T) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	rows := []AggregateRow{
		{ModelName: "priced", Day: day, PromptTokens: 1_000_000},
		{ModelName: "missing", Day: day, PromptTokens: 1, Quota: 250_000},
	}
	prices := []PriceRecord{{ModelName: "priced", Price: Price{EffectiveFrom: day, Input: "2", Output: "2", Cache: "2"}}}
	items := BuildDetails(rows, prices, nil, map[string]string{})
	if len(items) != 2 || items[1].PriceSource != "ct" || items[1].Amount != "2.000000" {
		t.Fatalf("unexpected priced item: %#v", items)
	}
	if items[0].Unpriced || items[0].ModelName != "missing" || items[0].PriceSource != "newapi" || items[0].Amount != "0.500000" {
		t.Fatalf("unexpected quota item: %#v", items)
	}
}

func TestParseRatioSnapshotDefaultsQuotaPerUnit(t *testing.T) {
	quotaPerUnit, err := quotaPerUnitForReport("")
	if err != nil || quotaPerUnit != defaultQuotaPerUnit {
		t.Fatalf("unexpected quota per unit: %q err=%v", quotaPerUnit, err)
	}
}

func TestAmountFromQuota(t *testing.T) {
	amount, err := AmountFromQuota(1_250_000, "500000")
	if err != nil || FormatAmount(amount, 6) != "2.500000" {
		t.Fatalf("unexpected amount: %v err=%v", amount, err)
	}
}
