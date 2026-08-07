package billing

import "testing"

func TestRequestPriceUsesPerLogCompletionRatio(t *testing.T) {
	log := PagedLogRecord{CompletionRatio: "4", CacheRatio: "0.5", GroupRatio: "1"}
	price, ratio := RequestPrice(log, Price{Input: "2.5", Output: "2.5", Cache: "1.25"})
	if price.Output != "10.000000" {
		t.Fatalf("output price = %s, want 10.000000", price.Output)
	}
	if price.Cache != "1.250000" || price.CacheWrite != "" || ratio != "1" {
		t.Fatalf("unexpected reconstructed price: %#v ratio=%s", price, ratio)
	}
}

func TestRequestPriceFallsBackForLegacyLog(t *testing.T) {
	fallback := Price{Input: "5", Output: "25", Cache: "0.5", CacheWrite: "6.25"}
	price, ratio := RequestPrice(PagedLogRecord{}, fallback)
	if price != fallback || ratio != "" {
		t.Fatalf("legacy price = %#v ratio=%q", price, ratio)
	}
}
