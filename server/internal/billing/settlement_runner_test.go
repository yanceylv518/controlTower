package billing

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

type settlementRunnerStore struct {
	sourceModeStore
	rows  int
	rules []StatementDiscount
}

func (s *settlementRunnerStore) ListBillingStatementDiscounts(context.Context, string) ([]StatementDiscount, error) {
	return s.rules, nil
}
func (s *settlementRunnerStore) AppendBillingHour(_ context.Context, _ Job, _ JobStep, _ []DailyRow, _ []TokenDailyRow, _ []ChannelDailyRow, d []RequestDetail, _ []AnomalyOrder, _ []ReconciliationOrder, _ LogCursor, _ int64) error {
	s.rows += len(d)
	s.details = append(s.details, d...)
	return nil
}

type shortArchivePages struct {
	at    time.Time
	calls int
}

func (s *shortArchivePages) LogsPage(_ context.Context, _ string, _, _ time.Time, c LogCursor, _ int) ([]PagedLogRecord, error) {
	s.calls++
	if s.calls > 5 {
		panic("cursor did not advance")
	}
	if c.ID >= 3 {
		return nil, nil
	}
	return []PagedLogRecord{{ID: c.ID + 1, CreatedUnix: s.at.Unix(), UserID: 7, ModelName: "m", Quota: 100, CompletionTokens: sql.NullInt64{Valid: true}}}, nil
}
func TestArchiveShortPagesDoNotTruncateAndSupplementUsesSnapshot(t *testing.T) {
	day := time.Date(2025, 9, 1, 0, 0, 0, 0, BusinessLocation)
	source := &shortArchivePages{at: day}
	store := &settlementRunnerStore{rules: []StatementDiscount{{DiscountType: DiscountUserModel, SubjectID: 7, ModelName: "m", Discount: "0.8", EffectiveFrom: day}}}
	runner := JobRunner{Store: store, Source: source}
	job := Job{ID: "job", InstanceID: "site", JobType: "user_statement", UserID: 0, DataSource: "archive", UsageVersion: 3, PricingSource: PricingSourceNewAPI}
	store.rules[0].SubjectID = 0
	if err := runner.processStep(context.Background(), job, JobStep{From: day, To: day.AddDate(0, 0, 1)}); err != nil {
		t.Fatal(err)
	}
	if store.rows != 3 || source.calls != 4 || !store.completed {
		t.Fatalf("rows=%d calls=%d completed=%v", store.rows, source.calls, store.completed)
	}
	for _, d := range store.details {
		if !d.EmptyOutput || d.Charge.Settlement == nil || d.Charge.Settlement.Source != "ct_supplement" {
			t.Fatalf("missing per-request settlement: %+v", d)
		}
	}
}
