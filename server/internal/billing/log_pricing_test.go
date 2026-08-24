package billing

import (
	"database/sql"
	"math/big"
	"testing"
)

func TestCalculateLogChargeConvertsRecordedRatiosToPrices(t *testing.T) {
	log := PagedLogRecord{
		PromptTokens: sql.NullInt64{Valid: true, Int64: 100}, CompletionTokens: sql.NullInt64{Valid: true, Int64: 10},
		ModelPrice: "-1", ModelRatio: "2.5", CompletionRatio: "4", GroupRatio: "1.2",
	}
	charge, err := CalculateLogCharge(log, "500000")
	if err != nil {
		t.Fatal(err)
	}
	if charge.Mode != "token" || charge.InputPrice != "6.000000" || charge.OutputPrice != "24.000000" {
		t.Fatalf("prices = %#v", charge)
	}
	if charge.InputAmount != "0.000600" || charge.OutputAmount != "0.000240" || charge.Total != "0.000840" || charge.ReconstructedQuota != "420.000000" {
		t.Fatalf("amounts = %#v", charge)
	}
}

func TestCalculateLogChargeUsesRecordedCacheWriteDurations(t *testing.T) {
	log := PagedLogRecord{
		PromptTokens: sql.NullInt64{Valid: true, Int64: 2}, CacheWriteTokens: 30, CacheWrite5mTokens: 10, CacheWrite1hTokens: 20,
		ModelPrice: "-1", ModelRatio: "2.5", GroupRatio: "1", CacheCreationRatio: "1.25", CacheCreationRatio5m: "1.25", CacheCreationRatio1h: "2",
	}
	charge, err := CalculateLogCharge(log, "500000")
	if err != nil {
		t.Fatal(err)
	}
	if charge.CacheWrite5mPrice != "6.250000" || charge.CacheWrite1hPrice != "10.000000" {
		t.Fatalf("cache prices = %#v", charge)
	}
	if charge.Total != "0.000273" || charge.ReconstructedQuota != "136.250000" {
		t.Fatalf("cache amounts = %#v", charge)
	}
}

func TestCalculateLogChargeSupportsFixedPerRequestPrice(t *testing.T) {
	charge, err := CalculateLogCharge(PagedLogRecord{ModelPrice: "0.02", GroupRatio: "1.5"}, "500000")
	if err != nil {
		t.Fatal(err)
	}
	if charge.Mode != "per_request" || charge.PerRequestPrice != "0.030000" || charge.Total != "0.030000" || charge.ReconstructedQuota != "15000.000000" {
		t.Fatalf("fixed charge = %#v", charge)
	}
}

func TestCalculateLogChargeRejectsIncompleteRecordedPricing(t *testing.T) {
	_, err := CalculateLogCharge(PagedLogRecord{PromptTokens: sql.NullInt64{Valid: true, Int64: 1}, ModelRatio: "2.5", GroupRatio: ""}, "500000")
	if err == nil {
		t.Fatal("expected missing group ratio to be unverifiable")
	}
	_, err = CalculateLogCharge(PagedLogRecord{CacheWriteTokens: 1, CacheWrite1hTokens: 1, ModelRatio: "2.5", GroupRatio: "1"}, "500000")
	if err == nil {
		t.Fatal("expected missing 1h cache ratio to be unverifiable")
	}
}

func TestVerifyLogChargeUsesNewAPIHalfAwayRounding(t *testing.T) {
	log := PagedLogRecord{ModelPrice: "0.000001", GroupRatio: "1", Quota: 1}
	result, err := VerifyLogCharge(log, "500000")
	if err != nil || !result.Verified || result.CalculatedQuota != 1 {
		t.Fatalf("half quota verification = %#v err=%v", result, err)
	}
	log.Quota = 0
	result, err = VerifyLogCharge(log, "500000")
	if err != nil || result.Verified || result.Difference != 1 {
		t.Fatalf("mismatch verification = %#v err=%v", result, err)
	}
}

func TestQuotaFromRatSaturatesLikeNewAPIInt32Storage(t *testing.T) {
	if got := quotaFromRat(big.NewRat(1<<32, 1)); got != 1<<31-1 {
		t.Fatalf("positive clamp = %d", got)
	}
	if got := quotaFromRat(big.NewRat(-1<<32, 1)); got != -1<<31 {
		t.Fatalf("negative clamp = %d", got)
	}
}

func TestVerifyLogChargeReasonSeparatesIncompleteAndMismatch(t *testing.T) {
	if _, reason := VerifyLogChargeReason(PagedLogRecord{}, "500000"); reason != PricingReasonIncomplete {
		t.Fatalf("incomplete reason = %q", reason)
	}
	log := PagedLogRecord{ModelPrice: "0.02", GroupRatio: "1", Quota: 999}
	if result, reason := VerifyLogChargeReason(log, "500000"); reason != PricingReasonMismatch || result.Difference != 9001 {
		t.Fatalf("mismatch = %#v reason=%q", result, reason)
	}
}
