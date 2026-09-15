package billing

import (
	"context"
	"database/sql"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistoricalPricesDoNotChangeLoggedAmount(t *testing.T) {
	job := Job{UsageVersion: HistoricalPriceUsageVersion, PricingSource: PricingSourceNewAPI}
	log := PagedLogRecord{Quota: 12345, ModelRatio: "10", CompletionRatio: "5", CacheRatio: "0.1", GroupRatio: "1", ImageRatio: "1"}
	got, reason, err := StatementLogCharge(job, log, "500000")
	if err != nil || reason != "" {
		t.Fatalf("%v %s", err, reason)
	}
	before := got
	attachHistoricalPrices(job, log, "500000", &got.Charge)
	if got.Charge.Total != before.Charge.Total || got.CalculatedQuota != before.CalculatedQuota || got.Charge.Mode != "newapi" {
		t.Fatalf("amount changed: %+v", got)
	}
	if got.Charge.InputPrice != "20.000000" || got.Charge.OutputPrice != "100.000000" || got.Charge.CacheReadPrice != "2.000000" || got.Charge.ImagePrice != "20.000000" {
		t.Fatalf("prices %+v", got.Charge)
	}
	if got.Charge.CacheWritePrice != "不适用（无用量）" {
		t.Fatal("missing price became zero")
	}
	log.CacheRatio = "0"
	attachHistoricalPrices(job, log, "500000", &got.Charge)
	if got.Charge.CacheReadPrice != "0.000000" {
		t.Fatal("free price lost")
	}
	log.CompletionRatio = "bad"
	attachHistoricalPrices(job, log, "500000", &got.Charge)
	if got.Charge.InputPrice != "20.000000" || got.Charge.OutputPrice != "未记录" {
		t.Fatal("partial snapshot lost")
	}
}

func TestHistoricalExpressionIsDisplayedNotExecuted(t *testing.T) {
	job := Job{UsageVersion: HistoricalPriceUsageVersion, PricingSource: PricingSourceNewAPI}
	for _, expr := range []string{`v1:p > 1000 ? tier("large", p * 20 + c * 100) : tier("small", p * 10 + c * 50)`, `unsupported_function(p)`} {
		log := PagedLogRecord{Quota: 99, BillingMode: "tiered_expr", ExprBase64: base64.StdEncoding.EncodeToString([]byte(expr)), MatchedTier: "small", RequestRules: `[{"cond":"header", "matched":true}]`, GroupRatio: "0.8"}
		got, reason, err := StatementLogCharge(job, log, "500000")
		if err != nil || reason != "" {
			t.Fatalf("%v %s", err, reason)
		}
		attachHistoricalPrices(job, log, "500000", &got.Charge)
		if got.Charge.Total != "0.000198000000" || got.Charge.InputPrice != "见计价规则" || !strings.Contains(got.Charge.PricingRule, expr) || !strings.Contains(got.Charge.PricingRule, log.RequestRules) {
			t.Fatalf("%+v", got.Charge)
		}
	}
	charge := LogCharge{Total: "2", InputPrice: "old"}
	attachHistoricalPrices(Job{UsageVersion: 1}, PagedLogRecord{}, "500000", &charge)
	if charge.InputPrice != "old" || charge.PricingRule != "" {
		t.Fatal("legacy job changed")
	}
}

func TestHistoricalFreePerRequestPriceAndCSV(t *testing.T) {
	job := Job{UsageVersion: HistoricalPriceUsageVersion, PricingSource: PricingSourceNewAPI}
	charge := LogCharge{Mode: "newapi", Total: "0"}
	attachHistoricalPrices(job, PagedLogRecord{ModelPrice: "0", GroupRatio: "1"}, "500000", &charge)
	if !hasPerRequestPrice(charge) || charge.PerRequestPrice != "0.000000" {
		t.Fatal("free per-request price hidden")
	}
	headers := detailCSVHeaders(job, []string{"金额"})
	cells := detailCSVCells(job, &MultimediaUsage{}, []string{"0"}, charge)
	if len(headers) != len(cells) || cells[len(cells)-1] != charge.PricingRule {
		t.Fatalf("CSV columns/rule mismatch: %v %v", headers, cells)
	}
	if len(detailCSVCells(job, nil, []string{"0"})) != len(headers) {
		t.Fatal("anomaly CSV columns mismatch")
	}
}

func TestHistoricalSourcePricesSurviveSpoolAndDailyFiles(t *testing.T) {
	day := time.Date(2026, 9, 15, 0, 0, 0, 0, BusinessLocation)
	log := PagedLogRecord{ID: 1, CreatedUnix: day.Unix(), UserID: 7, ModelName: "kimi-k3", Quota: 12345, PromptTokens: sql.NullInt64{Int64: 1000, Valid: true}, CompletionTokens: sql.NullInt64{Int64: 100, Valid: true}, ModelRatio: "20", CompletionRatio: "5", CacheRatio: "0.1", GroupRatio: "1"}
	job := Job{ID: "0123456789abcdef0123456789abcdef", InstanceID: "site", JobType: "user_statement", UserID: 7, PricingSource: PricingSourceNewAPI, UsageVersion: HistoricalPriceUsageVersion}
	store := &sourceModeStore{}
	spool := FileDetailSpool{Root: t.TempDir()}
	if err := (JobRunner{Store: store, Source: sourceModeLogs{[]PagedLogRecord{log}}, Spool: spool}).processStep(context.Background(), job, JobStep{StepNo: 1, From: day, To: day.AddDate(0, 0, 1)}); err != nil {
		t.Fatal(err)
	}
	if len(store.mismatches) != 0 || len(store.details) != 1 || store.details[0].Charge.InputPrice != "20.000000" {
		t.Fatalf("%+v", store)
	}
	fileStore := &dailyFileStoreStub{}
	root := t.TempDir()
	if err := (UserDailyFileGenerator{Store: fileStore, Root: root, Spool: spool}).GenerateJobFiles(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	content := workbookText(t, filepath.Join(root, filepath.FromSlash(fileStore.files[0].RelativePath)))
	for _, want := range []string{"历史计价规则", "20.000000", "100.000000", "2.000000", "0.012345000000", "计价规则"} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(content, "未拆分") {
		t.Fatal("new prices hidden")
	}
}

func TestHistoricalMissingOptionalPriceRequiresUsage(t *testing.T) {
	job := Job{UsageVersion: HistoricalPriceUsageVersion}
	for _, used := range []int64{0, 12} {
		log := PagedLogRecord{ModelRatio: "10", GroupRatio: "1", ImageInputTokens: used, CacheTokens: used, CacheWriteTokens: 3 * used, CacheWrite5mTokens: used, CacheWrite1hTokens: used}
		charge := LogCharge{Total: "123.456"}
		attachHistoricalPrices(job, log, "500000", &charge)
		want := "不适用（无用量）"
		if used > 0 {
			want = "有用量但未记录"
		}
		for _, price := range []string{charge.ImagePrice, charge.CacheReadPrice, charge.CacheWritePrice, charge.CacheWrite5mPrice, charge.CacheWrite1hPrice} {
			if price != want {
				t.Fatalf("used=%d price=%s want=%s", used, price, want)
			}
		}
		if charge.Total != "123.456" {
			t.Fatal("amount changed")
		}
	}
}
