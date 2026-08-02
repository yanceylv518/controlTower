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
	if price.Input != "4.000000000000" || price.Output != "12.000000000000" || price.Cache != "2.000000000000" || ratio != "1.2" {
		t.Fatalf("unexpected fallback: %#v ratio=%s", price, ratio)
	}
}

func TestBuildSummaryUsesCTThenSnapshotAndMarksUnpriced(t *testing.T) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	rows := []AggregateRow{
		{UserID: 7, Username: "alice", ModelName: "priced", GroupName: "vip", Day: day, PromptTokens: 1_000_000},
		{UserID: 7, Username: "alice", ModelName: "fallback", GroupName: "vip", Day: day, CompletionTokens: 1_000_000},
		{UserID: 7, Username: "alice", ModelName: "missing", GroupName: "vip", Day: day, PromptTokens: 100},
	}
	prices := []PriceRecord{{ModelName: "priced", Price: Price{EffectiveFrom: day, TierFrom: 0, Input: "2", Output: "4", Cache: "1"}}}
	ratios := []GroupRatio{{GroupName: "vip", Ratio: "1.5"}}
	snapshots := map[string]string{"2026-08-01": `{"ModelRatio":"{\"fallback\":2}","CompletionRatio":"{\"fallback\":3}","GroupRatio":"{\"vip\":1.2}","QuotaPerUnit":500000}`}
	items, total := BuildSummary(rows, prices, ratios, snapshots, map[int64]int64{7: 99})
	if len(items) != 1 || items[0].Amount != "17.400000" || total.Amount != "17.400000" {
		t.Fatalf("unexpected summary: %#v total=%#v", items, total)
	}
	if len(items[0].UnpricedModels) != 1 || items[0].UnpricedModels[0] != "missing" {
		t.Fatalf("unexpected unpriced models: %#v", items[0].UnpricedModels)
	}
	if len(items[0].PriceSources) != 2 || items[0].PriceSources[0] != "ct" || items[0].PriceSources[1] != "newapi" {
		t.Fatalf("unexpected sources: %#v", items[0].PriceSources)
	}
}
