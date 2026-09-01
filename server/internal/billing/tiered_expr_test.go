package billing

import (
	"database/sql"
	"encoding/base64"
	"testing"
	"time"
)

func tieredLog(exprText string, at time.Time) PagedLogRecord {
	return PagedLogRecord{
		CreatedUnix: at.Unix(), BillingMode: "tiered_expr", ExprBase64: base64.StdEncoding.EncodeToString([]byte(exprText)),
		MatchedTier: "peak", GroupRatio: "1", SourcePromptTokens: sql.NullInt64{Int64: 25, Valid: true},
		PromptTokens: sql.NullInt64{Int64: 25, Valid: true}, CompletionTokens: sql.NullInt64{Int64: 86, Valid: true},
	}
}

func TestTieredExpressionUsesLogTimeForPeakPrice(t *testing.T) {
	expression := `hour("Asia/Shanghai") >= 8 && hour("Asia/Shanghai") < 20 ? tier("peak", p * 20 + c * 100) : tier("offpeak", p * 10 + c * 50)`
	peak := time.Date(2026, 8, 24, 10, 0, 0, 0, BusinessLocation)
	log := tieredLog(expression, peak)
	log.Quota = 4550
	result, err := VerifyLogCharge(log, "500000")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || result.Charge.Total != "0.009100" || result.Charge.MatchedTier != "peak" {
		t.Fatalf("unexpected peak charge: %+v", result)
	}
	if result.Charge.InputPrice != "20.000000" || result.Charge.OutputPrice != "100.000000" {
		t.Fatalf("unexpected peak unit prices: %+v", result.Charge)
	}

	offPeak := time.Date(2026, 8, 24, 2, 0, 0, 0, BusinessLocation)
	log = tieredLog(expression, offPeak)
	log.MatchedTier, log.Quota = "offpeak", 2275
	result, err = VerifyLogCharge(log, "500000")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || result.Charge.Total != "0.004550" || result.Charge.MatchedTier != "offpeak" {
		t.Fatalf("unexpected off-peak charge: %+v", result)
	}
	if result.Charge.InputPrice != "10.000000" || result.Charge.OutputPrice != "50.000000" {
		t.Fatalf("unexpected off-peak unit prices: %+v", result.Charge)
	}
}

func TestTieredExpressionUsesEmbeddedShanghaiTimezone(t *testing.T) {
	expression := `
(
  weekday("Asia/Shanghai") >= 1 &&
  weekday("Asia/Shanghai") <= 5 &&
  (
    (hour("Asia/Shanghai") >= 9 && hour("Asia/Shanghai") < 12) ||
    (hour("Asia/Shanghai") >= 14 && hour("Asia/Shanghai") < 18)
  )
)
? tier("高峰时段", p * 9 + c * 27 + cr * 0.3)
: tier("空闲时段", p * 4.5 + c * 13.5 + cr * 0.15)`
	log := tieredLog(expression, time.Date(2026, 8, 18, 15, 55, 4, 0, BusinessLocation))
	log.SourcePromptTokens = sql.NullInt64{Int64: 5, Valid: true}
	log.PromptTokens = sql.NullInt64{Int64: 5, Valid: true}
	log.CompletionTokens = sql.NullInt64{Int64: 10, Valid: true}
	log.MatchedTier = "高峰时段"
	log.Quota = 158

	result, err := VerifyLogCharge(log, "500000")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || result.CalculatedQuota != 158 {
		t.Fatalf("unexpected verification: %+v", result)
	}
	if result.Charge.MatchedTier != "高峰时段" || result.Charge.Total != "0.000315" {
		t.Fatalf("unexpected tiered charge: %+v", result.Charge)
	}
}

func TestTieredExpressionIncludesToolSurcharge(t *testing.T) {
	log := tieredLog(`tier("空闲时段", p * 4.5 + c * 13.5)`, time.Date(2026, 8, 31, 13, 21, 21, 0, BusinessLocation))
	log.SourcePromptTokens = sql.NullInt64{Int64: 920597, Valid: true}
	log.PromptTokens = sql.NullInt64{Int64: 920597, Valid: true}
	log.CompletionTokens = sql.NullInt64{Int64: 2000, Valid: true}
	log.MatchedTier = "空闲时段"
	log.ToolSurcharges = `[{"name":"web_search","count":1,"price":10}]`
	log.Quota = 2089843

	result, err := VerifyLogCharge(log, "500000")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || result.Charge.ToolSurchargeAmount != "0.010000" {
		t.Fatalf("unexpected tool surcharge charge: %+v", result)
	}
}

func TestTieredExpressionUsesFrozenRequestRuleTrace(t *testing.T) {
	log := tieredLog(`tier("peak", p * 20) * (has(header("anthropic-beta"), "fast-mode") ? 2 : 1)`, time.Date(2026, 8, 24, 10, 0, 0, 0, BusinessLocation))
	log.CompletionTokens = sql.NullInt64{}
	log.RequestRules = `[{"cond":"header(anthropic-beta) has fast-mode","multiplier":2,"matched":true}]`
	log.Quota = 500
	result, err := VerifyLogCharge(log, "500000")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || result.Charge.Total != "0.001000" {
		t.Fatalf("unexpected rule charge: %+v", result)
	}
}

func TestTieredExpressionNeverFallsBackToRatios(t *testing.T) {
	log := tieredLog(`not valid`, time.Now())
	log.ModelRatio, log.CompletionRatio = "2", "5"
	if _, reason := VerifyLogChargeReason(log, "500000"); reason != PricingReasonIncomplete {
		t.Fatalf("reason = %q, want %q", reason, PricingReasonIncomplete)
	}
}

func TestTieredExpressionExposesUnusedCacheReadUnitPrice(t *testing.T) {
	log := tieredLog(`tier("peak", p * 1.5 + c * 4.5 + cr * 0.3 + cc * 1.8)`, time.Date(2026, 8, 24, 10, 0, 0, 0, BusinessLocation))
	log.SourcePromptTokens = sql.NullInt64{Int64: 5, Valid: true}
	log.PromptTokens = sql.NullInt64{Int64: 5, Valid: true}
	log.CompletionTokens = sql.NullInt64{Int64: 10, Valid: true}
	log.CacheTokens, log.CacheWriteTokens = 0, 0
	log.Quota = 26
	result, err := VerifyLogCharge(log, "500000")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatalf("charge was not verified: %+v", result)
	}
	if result.Charge.CacheReadPrice != "0.300000" || result.Charge.CacheReadAmount != "0.000000" {
		t.Fatalf("unexpected zero-usage cache read pricing: %+v", result.Charge)
	}
	if result.Charge.CacheWritePrice != "1.800000" || result.Charge.CacheWriteAmount != "0.000000" {
		t.Fatalf("unexpected zero-usage cache write pricing: %+v", result.Charge)
	}
}
