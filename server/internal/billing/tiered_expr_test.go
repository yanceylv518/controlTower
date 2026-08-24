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
