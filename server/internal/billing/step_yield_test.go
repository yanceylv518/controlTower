package billing

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

type yieldingStore struct {
	sourceModeStore
	cursor                                 LogCursor
	rows, bad, completedRows, completedBad int64
	yields                                 int
}

func (s *yieldingStore) AppendBillingHour(_ context.Context, _ Job, _ JobStep, _ []DailyRow, _ []TokenDailyRow, _ []ChannelDailyRow, d []RequestDetail, a []AnomalyOrder, _ []ReconciliationOrder, c LogCursor, n int64) error {
	s.cursor = c
	s.rows += n
	s.bad += int64(len(a))
	s.details = append(s.details, d...)
	return nil
}
func (s *yieldingStore) YieldBillingStep(context.Context, Job, JobStep) error { s.yields++; return nil }
func (s *yieldingStore) CompleteBillingStep(_ context.Context, _ Job, _ JobStep, n, b int64) error {
	s.completed = true
	s.completedRows = n
	s.completedBad = b
	return nil
}

type yieldingSource struct {
	sourceModeLogs
	at int64
}

func (s yieldingSource) LogsPage(_ context.Context, _ string, _, _ time.Time, c LogCursor, n int) ([]PagedLogRecord, error) {
	var out []PagedLogRecord
	for id := c.ID + 1; id <= BillingPageSize+1 && len(out) < n; id++ {
		output := sql.NullInt64{Valid: id != 1, Int64: 2}
		out = append(out, PagedLogRecord{ID: id, CreatedUnix: s.at, UserID: 7, ModelName: "m", Quota: 100, PromptTokens: sql.NullInt64{Valid: true, Int64: 10}, CompletionTokens: output})
	}
	return out, nil
}
func TestBillingStepYieldsAfterDurablePageAndResumesSameSecond(t *testing.T) {
	day := time.Date(2025, 1, 1, 0, 0, 0, 0, BusinessLocation)
	store := &yieldingStore{}
	spool := FileDetailSpool{Root: t.TempDir()}
	job := Job{ID: "0123456789abcdef0123456789abcdef", InstanceID: "a", JobType: "generate", UsageVersion: 1, PricingSource: PricingSourceNewAPI}
	r := JobRunner{Store: store, Source: yieldingSource{at: day.Unix()}, Spool: spool, StepQuantum: time.Nanosecond}
	step := JobStep{From: day, To: day.AddDate(0, 0, 1)}
	if err := r.processStep(context.Background(), job, step); err != nil {
		t.Fatal(err)
	}
	if store.yields != 1 || store.completed || store.rows != BillingPageSize || store.cursor.ID != BillingPageSize {
		t.Fatal(store.yields, store.completed, store.rows, store.cursor)
	}
	step.Cursor, step.ProcessedRows, step.AbnormalRows = store.cursor, store.rows, store.bad
	if err := r.processStep(context.Background(), job, step); err != nil {
		t.Fatal(err)
	}
	if !store.completed || store.rows != BillingPageSize+1 || store.completedRows != store.rows || store.completedBad != store.bad || store.bad != 1 {
		t.Fatal(store.rows, store.completedRows, store.bad, store.completedBad)
	}
	pages, err := spool.OpenPages(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, page := range pages {
		if err = page.Read(func(d RequestDetail) error {
			if seen[d.SourceLogID] {
				t.Fatal("duplicate spool record", d.SourceLogID)
			}
			seen[d.SourceLogID] = true
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != BillingPageSize || seen[1] {
		t.Fatal("lost spool records", len(seen))
	}
}
