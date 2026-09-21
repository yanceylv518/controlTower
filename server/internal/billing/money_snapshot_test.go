package billing

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMoneySnapshotValidationAndEvidence(t *testing.T) {
	now := time.Now()
	for _, raw := range []string{`{}`, `{"QuotaPerUnit":"0"}`, `{"QuotaPerUnit":"-1"}`, `{"QuotaPerUnit":"nan"}`, `{"QuotaPerUnit":"500000","general_setting.quota_display_type":"UNKNOWN"}`, `{"QuotaPerUnit":"500000","general_setting.quota_display_type":"CNY","USDExchangeRate":"0"}`} {
		if _, err := NewMoneySnapshot("site", raw, now); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	s, err := NewMoneySnapshot("site", `{"QuotaPerUnit":"500000.000000000001","ModelRatio":"secret","general_setting":"{\"quota_display_type\":\"CUSTOM\",\"custom_currency_exchange_rate\":2.5,\"custom_currency_symbol\":\"X\",\"unrelated_secret\":\"secret\"}"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.Evidence, "secret") || s.QuotaPerUnit != "500000.000000000001" || s.Display.ExchangeRate != "2.5" {
		t.Fatalf("snapshot=%+v", s)
	}
	if err = s.Validate("site"); err != nil {
		t.Fatal(err)
	}
	if s.Validate("another-site") == nil {
		t.Fatal("cross-site accepted")
	}
	s.QuotaPerUnit = "1"
	if s.Validate("site") == nil {
		t.Fatal("tampered snapshot accepted")
	}
}

type pinnedMoneyStore struct {
	sourceModeStore
	snapshot    *MoneySnapshot
	finalized   bool
	anomalyUnit string
}

func (s *pinnedMoneyStore) BillingJobMoneySnapshot(context.Context, string) (*MoneySnapshot, error) {
	return s.snapshot, nil
}
func (s *pinnedMoneyStore) FinalizeBillingJob(context.Context, Job) error {
	s.finalized = true
	return nil
}
func (s *pinnedMoneyStore) UpdateBillingAnomalyActualAmounts(_ context.Context, _ string, unit string) error {
	s.anomalyUnit = unit
	return nil
}

type unavailableMoneySource struct{ sourceModeLogs }

func (unavailableMoneySource) RatioSnapshot(context.Context, string) (string, error) {
	panic("pinned bill read live monetary options")
}
func (unavailableMoneySource) Balances(context.Context, string) (map[int64]int64, error) {
	panic("pinned bill read current balances")
}

func TestPinnedMoneyRetryPublishWorkbook(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2026, 9, 20, 0, 0, 0, 0, BusinessLocation)
	snapshot, err := NewMoneySnapshot("site", `{"QuotaPerUnit":"500000","general_setting.quota_display_type":"CNY","USDExchangeRate":"7.3"}`, day)
	if err != nil {
		t.Fatal(err)
	}
	job := Job{ID: "0123456789abcdef0123456789abcdef", InstanceID: "site", JobType: "user_statement", UserID: 7, PricingSource: PricingSourceNewAPI, UsageVersion: 1, ExcludeZeroOutput: true, From: day, To: day.AddDate(0, 0, 1)}
	log := PagedLogRecord{ID: 1, CreatedUnix: day.Unix(), UserID: 7, Quota: 1, PromptTokens: sql.NullInt64{Valid: true, Int64: 1}, CompletionTokens: sql.NullInt64{Valid: true, Int64: 1}}
	excluded := log
	excluded.ID = 2
	excluded.Quota = 999999
	excluded.CompletionTokens.Int64 = 0
	store := &pinnedMoneyStore{snapshot: snapshot}
	spool := FileDetailSpool{Root: t.TempDir()}
	files := &dailyFileStoreStub{}
	root := t.TempDir()
	runner := JobRunner{Store: store, Source: unavailableMoneySource{sourceModeLogs{[]PagedLogRecord{log, excluded}}}, Spool: spool, Files: UserDailyFileGenerator{Store: files, Root: root, Spool: spool}}
	step := JobStep{StepNo: 1, From: job.From, To: job.To}
	for i := 0; i < 2; i++ {
		if err = runner.processStep(ctx, job, step); err != nil {
			t.Fatal(err)
		}
		if len(store.details) != 1 || store.details[0].Charge.Total != "0.000002000000" {
			t.Fatalf("details=%+v", store.details)
		}
	}
	if err = runner.publishJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if !store.finalized || store.anomalyUnit != "500000" || len(files.files) != 1 {
		t.Fatalf("publish=%+v files=%+v", store, files.files)
	}
	content := workbookText(t, filepath.Join(root, filepath.FromSlash(files.files[0].RelativePath)))
	if !strings.Contains(content, "0.000002000000") || strings.Contains(content, "999999") {
		t.Fatal("workbook amount or zero-output scope incorrect")
	}
	store.snapshot = nil
	if runner.processStep(ctx, job, step) == nil || runner.publishJob(ctx, job) == nil {
		t.Fatal("unbound old task silently repriced")
	}
}
