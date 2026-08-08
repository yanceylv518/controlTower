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
	if price.Input != "4.000000000000" || price.Output != "12.000000000000" || price.Cache != "2.000000000000" || price.CacheWrite != "5.000000000000" || ratio != "1.2" {
		t.Fatalf("unexpected fallback: %#v ratio=%s", price, ratio)
	}
}

func TestFallbackPriceUsesNewAPIDefaultCacheWritePrice(t *testing.T) {
	snapshot, err := ParseRatioSnapshot(`{"ModelRatio":"{\"gpt-test\":2}","QuotaPerUnit":500000}`)
	if err != nil {
		t.Fatal(err)
	}
	price, _, err := FallbackPrice(snapshot, "gpt-test", "")
	if err != nil || price.CacheWrite != "5.000000000000" {
		t.Fatalf("unexpected unconfigured cache write price: %#v err=%v", price, err)
	}
}

func TestFallbackPriceUsesNewAPIBuiltInCompletionRatio(t *testing.T) {
	snapshot, err := ParseRatioSnapshot(`{"ModelRatio":"{\"gpt-4o\":1.25,\"gpt-5.5\":2.5,\"claude-opus-4-8\":2.5}","QuotaPerUnit":500000}`)
	if err != nil {
		t.Fatal(err)
	}
	for model, want := range map[string]string{"gpt-4o": "10.000000000000", "gpt-5.5": "30.000000000000", "claude-opus-4-8": "25.000000000000"} {
		price, _, priceErr := FallbackPrice(snapshot, model, "")
		if priceErr != nil || price.Output != want {
			t.Fatalf("%s output=%s err=%v want=%s", model, price.Output, priceErr, want)
		}
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

func TestBuildDetailsUsesNewAPIDefaultCacheWriteWithoutExplicitRatio(t *testing.T) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	rows := []AggregateRow{{ModelName: "priced", Day: day, CacheWriteTokens: 1_000_000}}
	prices := []PriceRecord{{ModelName: "priced", Price: Price{EffectiveFrom: day, Input: "10", Output: "20", Cache: "1", CacheWrite: "12.5"}}}
	snapshots := map[string]string{"2026-08-01": `{"ModelRatio":"{\"priced\":2}","QuotaPerUnit":500000}`}
	items := BuildDetails(rows, prices, nil, snapshots)
	if len(items) != 1 || items[0].CacheWritePrice != "12.500000" || items[0].Amount != "12.500000" {
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

func TestParseRatioSnapshotCurrencyDisplay(t *testing.T) {
	raw := `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting":"{\"quota_display_type\":\"CNY\",\"custom_currency_symbol\":\"¤\",\"custom_currency_exchange_rate\":1}"}`
	snapshot, err := ParseRatioSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Currency.Type != "CNY" || snapshot.Currency.Symbol != "¥" || snapshot.Currency.ExchangeRate != "7.2" {
		t.Fatalf("unexpected currency: %#v", snapshot.Currency)
	}
}

func TestCurrencyDisplayForSnapshotsUsesNewestDay(t *testing.T) {
	snapshots := map[string]string{
		"2026-08-01": `{"QuotaPerUnit":"500000","general_setting":"{\"quota_display_type\":\"CNY\"}"}`,
		"2026-08-02": `{"QuotaPerUnit":"500000","general_setting":"{\"quota_display_type\":\"CUSTOM\",\"custom_currency_symbol\":\"HK$\",\"custom_currency_exchange_rate\":7.8}"}`,
	}
	currency := CurrencyDisplayForSnapshots(snapshots)
	if currency.Type != "CUSTOM" || currency.Symbol != "HK$" || currency.ExchangeRate != "7.8" {
		t.Fatalf("unexpected currency: %#v", currency)
	}
}

func TestAmountFromQuota(t *testing.T) {
	amount, err := AmountFromQuota(1_250_000, "500000")
	if err != nil || FormatAmount(amount, 6) != "2.500000" {
		t.Fatalf("unexpected amount: %v err=%v", amount, err)
	}
}

// The authoritative storage shape, verified against new-api source
// (setting/config/config.go SaveToDB): one dotted option key per field with
// bare values. There is no single "general_setting" JSON blob in options.
func TestParseRatioSnapshotCurrencyFromDottedKeys(t *testing.T) {
	raw := `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting.quota_display_type":"CNY"}`
	snapshot, err := ParseRatioSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Currency.Type != "CNY" || snapshot.Currency.Symbol != "¥" || snapshot.Currency.ExchangeRate != "7.2" {
		t.Fatalf("unexpected CNY currency: %#v", snapshot.Currency)
	}
	custom := `{"QuotaPerUnit":"500000","general_setting.quota_display_type":"CUSTOM","general_setting.custom_currency_symbol":"HK$","general_setting.custom_currency_exchange_rate":"7.8"}`
	snapshot, err = ParseRatioSnapshot(custom)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Currency.Type != "CUSTOM" || snapshot.Currency.Symbol != "HK$" || snapshot.Currency.ExchangeRate != "7.8" {
		t.Fatalf("unexpected custom currency: %#v", snapshot.Currency)
	}
	tokens := `{"QuotaPerUnit":"500000","general_setting.quota_display_type":"TOKENS"}`
	snapshot, err = ParseRatioSnapshot(tokens)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Currency.Type != "TOKENS" || snapshot.Currency.Symbol != "" {
		t.Fatalf("unexpected tokens display: %#v", snapshot.Currency)
	}
}
