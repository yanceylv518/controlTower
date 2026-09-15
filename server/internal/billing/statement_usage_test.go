package billing

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatementLogChargeSourceIgnoresPricing(t *testing.T) {
	for _, mode := range []string{"", "tiered_expr", "per_request"} {
		log := PagedLogRecord{Quota: 12345, BillingMode: mode, ExprBase64: "invalid!", ModelRatio: "invalid", GroupRatio: "", ImageInputTokens: 900}
		got, reason, err := StatementLogCharge(Job{PricingSource: PricingSourceNewAPI}, log, "1000000")
		if err != nil || reason != "" || got.Charge.Total != "0.012345000000" || got.CalculatedQuota != 12345 || got.Charge.Mode != "newapi" || got.Charge.InputPrice != "" {
			t.Fatalf("mode=%s got=%+v reason=%s err=%v", mode, got, reason, err)
		}
	}
	for _, unit := range []string{"0", "-1", "invalid"} {
		if _, _, err := StatementLogCharge(Job{PricingSource: PricingSourceNewAPI}, PagedLogRecord{Quota: 1}, unit); err == nil {
			t.Fatalf("accepted unit %q", unit)
		}
	}
}

func TestStatementLogChargeRecalculateAndLegacy(t *testing.T) {
	log := PagedLogRecord{Quota: 123, PromptTokens: sql.NullInt64{Int64: 1000000, Valid: true}, ModelRatio: "1", GroupRatio: "1"}
	for _, mode := range []string{"", PricingSourceRecalculate} {
		got, reason, err := StatementLogCharge(Job{PricingSource: mode}, log, "500000")
		if err != nil || reason != PricingReasonMismatch || got.CalculatedQuota != 1000000 || got.Charge.Total != "2.000000" {
			t.Fatalf("got=%+v reason=%s err=%v", got, reason, err)
		}
	}
}

type sourceModeStore struct {
	JobStore
	details    []RequestDetail
	anomalies  []AnomalyOrder
	mismatches []ReconciliationOrder
	completed  bool
}

func (s *sourceModeStore) ListBillingModelMetadata(context.Context, string) ([]ModelMetadata, error) {
	return nil, nil
}
func (s *sourceModeStore) AppendBillingHour(_ context.Context, _ Job, _ JobStep, _ []DailyRow, _ []TokenDailyRow, _ []ChannelDailyRow, d []RequestDetail, a []AnomalyOrder, m []ReconciliationOrder, _ LogCursor, _ int64) error {
	s.details = d
	s.anomalies = a
	s.mismatches = m
	return nil
}
func (s *sourceModeStore) CompleteBillingStep(context.Context, Job, JobStep, int64, int64) error {
	s.completed = true
	return nil
}

type sourceModeLogs struct{ logs []PagedLogRecord }

func (s sourceModeLogs) LogsPage(context.Context, string, time.Time, time.Time, LogCursor, int) ([]PagedLogRecord, error) {
	return s.logs, nil
}
func (s sourceModeLogs) DetailedLogsPage(context.Context, string, int64, time.Time, time.Time, LogCursor, int) ([]PagedLogRecord, error) {
	return s.logs, nil
}
func (s sourceModeLogs) RatioSnapshot(context.Context, string) (string, error) {
	return `{"QuotaPerUnit":1000000,"ModelRatio":"invalid"}`, nil
}
func (s sourceModeLogs) Balances(context.Context, string) (map[int64]int64, error) { return nil, nil }

func TestSourceModeRunnerSpoolAndWorkbook(t *testing.T) {
	day := time.Date(2026, 9, 15, 0, 0, 0, 0, BusinessLocation)
	log := PagedLogRecord{ID: 1, CreatedUnix: day.Unix(), UserID: 7, ModelName: "multimedia", Quota: 12345, PromptTokens: sql.NullInt64{Int64: 120, Valid: true}, CompletionTokens: sql.NullInt64{Int64: 90, Valid: true}, ImageInputTokens: 70, ImageOutputTokens: 20, AudioInputTokens: 30, AudioOutputTokens: 10, CacheTokens: 40}
	job := Job{ID: "0123456789abcdef0123456789abcdef", InstanceID: "site", JobType: "user_statement", UserID: 7, PricingSource: PricingSourceNewAPI, UsageVersion: 1}
	spool := FileDetailSpool{Root: t.TempDir()}
	store := &sourceModeStore{}
	runner := JobRunner{Store: store, Source: sourceModeLogs{[]PagedLogRecord{log}}, Spool: spool}
	if err := runner.processStep(context.Background(), job, JobStep{StepNo: 1, From: day, To: day.AddDate(0, 0, 1)}); err != nil {
		t.Fatal(err)
	}
	if !store.completed || len(store.details) != 1 || len(store.mismatches) != 0 || len(store.anomalies) != 0 {
		t.Fatalf("store=%+v", store)
	}
	d := store.details[0]
	if d.PromptTokens != 90 || d.CompletionTokens != 60 || d.ImageInputTokens != 70 || d.AudioInputTokens != 30 || d.ImageOutputTokens != 20 || d.AudioOutputTokens != 10 || d.Charge.Total != "0.012345000000" {
		t.Fatalf("detail=%+v", d)
	}
	pages, err := spool.OpenPages(context.Background(), job)
	if err != nil || len(pages) != 1 {
		t.Fatalf("pages=%v err=%v", pages, err)
	}
	if err = pages[0].Read(func(got RequestDetail) error {
		if got.MultimediaUsage != d.MultimediaUsage || got.Charge != d.Charge {
			t.Fatalf("spool=%+v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fileStore := &dailyFileStoreStub{}
	root := t.TempDir()
	if err = (UserDailyFileGenerator{Store: fileStore, Root: root, Spool: spool}).GenerateJobFiles(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if len(fileStore.files) != 1 {
		t.Fatalf("files=%v", fileStore.files)
	}
	content := workbookText(t, filepath.Join(root, filepath.FromSlash(fileStore.files[0].RelativePath)))
	for _, want := range []string{"普通输入 Token", "图像输入 Token", "图像输出 Token", "音频输入 Token", "音频输出 Token", "NewAPI 原始计费", "未拆分", "0.012345000000"} {
		if !strings.Contains(content, want) {
			t.Fatalf("workbook missing %q", want)
		}
	}
}

func TestStatementDisplayUsageClampsAndPreservesPricing(t *testing.T) {
	log := PagedLogRecord{PromptTokens: sql.NullInt64{Int64: 3, Valid: true}, CompletionTokens: sql.NullInt64{Int64: 2, Valid: true}, AudioInputTokens: 10, ImageOutputTokens: 20}
	p, c, _ := statementDisplayUsage(log)
	if p != 0 || c != 0 || log.PromptTokens.Int64 != 3 || log.CompletionTokens.Int64 != 2 {
		t.Fatalf("p=%d c=%d log=%+v", p, c, log)
	}
}
