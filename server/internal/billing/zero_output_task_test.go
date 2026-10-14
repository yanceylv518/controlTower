package billing

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type zeroOutputFileStore struct {
	dailyFileStoreStub
	expected int64
}

func (s *zeroOutputFileStore) ListBillingJobSteps(context.Context, string) ([]JobStep, error) {
	return []JobStep{{Status: "complete", ProcessedRows: s.expected}}, nil
}

func TestZeroOutputTaskFiltersAndPublishesOnlyRequestedRows(t *testing.T) {
	day := time.Date(2025, 9, 1, 0, 0, 0, 0, BusinessLocation)
	for _, kind := range []string{"user_statement", "upstream_statement"} {
		t.Run(kind, func(t *testing.T) {
			logs := []PagedLogRecord{
				{ID: 1, CreatedUnix: day.Unix(), UserID: 7, ChannelID: 1, ModelName: "m", Quota: 100, CompletionTokens: sql.NullInt64{Valid: true}},
				{ID: 2, CreatedUnix: day.Unix(), UserID: 7, ChannelID: 1, ModelName: "m", Quota: 200, CompletionTokens: sql.NullInt64{Valid: true, Int64: 2}},
				{ID: 3, CreatedUnix: day.Unix(), UserID: 7, ChannelID: 1, ModelName: "m", Quota: 300},
				{ID: 4, CreatedUnix: day.Unix(), UserID: 7, ChannelID: 1, ModelName: "unbound", Quota: 400, CompletionTokens: sql.NullInt64{Valid: true}},
			}
			store := &upstreamFilterStore{models: map[int64][]string{1: {"m"}}}
			var source PageSource = &upstreamFilterPages{logs: logs}
			want := 1
			if kind == "user_statement" {
				source = sourceModeLogs{logs: logs}
				want = 2
				store.models = nil
			}
			job := Job{ID: "0123456789abcdef0123456789abcdef", InstanceID: "site", JobType: kind, UsageVersion: 3, BillPeriod: ZeroOutputPeriod, PricingSource: PricingSourceNewAPI, From: day, To: day.AddDate(0, 0, 1)}
			spool := FileDetailSpool{Root: t.TempDir()}
			if err := (JobRunner{Store: store, Source: source, Spool: spool}).processStep(context.Background(), job, JobStep{From: job.From, To: job.To}); err != nil {
				t.Fatal(err)
			}
			if len(store.details) != want || !store.completed {
				t.Fatalf("details=%+v completed=%v", store.details, store.completed)
			}
			for _, d := range store.details {
				if !d.EmptyOutput || d.SourceLogID == 2 || d.SourceLogID == 3 {
					t.Fatal(d)
				}
			}
			root := t.TempDir()
			files := &zeroOutputFileStore{expected: int64(want)}
			gen := UserDailyFileGenerator{Store: files, Root: root, Spool: spool}
			for i := 0; i < 2; i++ {
				if err := gen.GenerateJobFiles(context.Background(), job); err != nil {
					t.Fatal(err)
				}
			}
			if len(files.files) != 2 || files.files[0].SHA256 == "" || files.files[0].RelativePath != files.files[1].RelativePath {
				t.Fatal(files.files)
			}
			xml := workbookText(t, filepath.Join(root, files.files[0].RelativePath))
			if !strings.Contains(xml, "空输出订单") || strings.Contains(xml, ">unbound<") && kind == "upstream_statement" {
				t.Fatal("incorrect export")
			}
			files.expected++
			if err := gen.GenerateJobFiles(context.Background(), job); err == nil || !strings.Contains(err.Error(), "count mismatch") {
				t.Fatal("incomplete spool published", err)
			}
			if len(files.files) != 2 {
				t.Fatal("incomplete export registered")
			}
		})
	}
}

func TestZeroOutputContextDoesNotAffectOrdinaryJobs(t *testing.T) {
	ctx := context.Background()
	if ZeroOutputOnly(ctx) || !ZeroOutputOnly(WithZeroOutputOnly(ctx)) {
		t.Fatal("filter context escaped")
	}
}
