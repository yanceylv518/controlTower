package billing

import (
	"database/sql"
	"testing"
	"time"
)

func TestNewJobSplitsRangeIntoHourlySteps(t *testing.T) {
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	job, steps, err := NewJob("site-a", from, from.Add(25*time.Hour), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if job.TotalSteps != 25 || len(steps) != 25 {
		t.Fatalf("steps=%d total=%d", len(steps), job.TotalSteps)
	}
	if steps[24].To.Sub(steps[24].From) != time.Hour {
		t.Fatalf("last step=%s", steps[24].To.Sub(steps[24].From))
	}
}

func TestNewJobPreservesShanghaiRangeAsUTCInstants(t *testing.T) {
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, BusinessLocation)
	_, steps, err := NewJob("site-a", from, from.Add(time.Hour), "admin")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 30, 16, 0, 0, 0, time.UTC)
	if !steps[0].From.UTC().Equal(want) {
		t.Fatalf("step UTC start=%s, want %s", steps[0].From.UTC(), want)
	}
}

func TestNewJobAlignsPartialStepsWithoutCrossingMidnight(t *testing.T) {
	from := time.Date(2026, 8, 1, 23, 30, 0, 0, time.Local)
	job, steps, err := NewJob("site-a", from, from.Add(time.Hour), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if job.TotalSteps != 2 || len(steps) != 2 {
		t.Fatalf("steps=%d total=%d", len(steps), job.TotalSteps)
	}
	if steps[0].To.Hour() != 0 || steps[0].To.Day() != 2 || !steps[0].To.Equal(steps[1].From) {
		t.Fatalf("unexpected step boundary: %#v", steps)
	}
}

func TestNewJobRejectsMoreThanSixtyDays(t *testing.T) {
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	if _, _, err := NewJob("site-a", from, from.AddDate(0, 0, 61), "admin"); err == nil {
		t.Fatal("expected a range longer than 60 days to be rejected")
	}
	if _, steps, err := NewJob("site-a", from, from.AddDate(0, 0, 60), "admin"); err != nil || len(steps) != 60*24 {
		t.Fatalf("60-day range should be accepted: steps=%d err=%v", len(steps), err)
	}
}

func TestAnomalyReasons(t *testing.T) {
	cases := []struct {
		name string
		log  PagedLogRecord
		max  int64
		want string
	}{
		{"missing", PagedLogRecord{}, 100, "input_token_missing,output_token_missing"},
		{"zero", PagedLogRecord{PromptTokens: sql.NullInt64{Valid: true}, CompletionTokens: sql.NullInt64{Valid: true}}, 100, "input_token_zero,output_token_zero"},
		{"context", PagedLogRecord{PromptTokens: sql.NullInt64{Valid: true, Int64: 101}, CompletionTokens: sql.NullInt64{Valid: true, Int64: 1}}, 100, "context_limit_exceeded"},
		{"fully cached input remains valid", PagedLogRecord{SourcePromptTokens: sql.NullInt64{Valid: true, Int64: 100}, PromptTokens: sql.NullInt64{Valid: true, Int64: 0}, CompletionTokens: sql.NullInt64{Valid: true, Int64: 1}, ContextTokens: 100}, 100, ""},
		{"unknown context accepted", PagedLogRecord{PromptTokens: sql.NullInt64{Valid: true, Int64: 1000000}, CompletionTokens: sql.NullInt64{Valid: true, Int64: 1}}, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ""
			for i, v := range AnomalyReasons(tc.log, tc.max) {
				if i > 0 {
					got += ","
				}
				got += v
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestFillAnomalyAmountsSeparatesCachedInput(t *testing.T) {
	log := PagedLogRecord{PromptTokens: sql.NullInt64{Valid: true, Int64: 100}, CompletionTokens: sql.NullInt64{Valid: true, Int64: 20}, CacheTokens: 40}
	prices := []Price{{EffectiveFrom: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), TierFrom: 0, Input: "1", Output: "2", Cache: "0.5"}}
	var got AnomalyOrder
	fillAnomalyAmounts(&got, log, prices, "1", time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC), true)
	if got.InputAmount != "0.000100" || got.OutputAmount != "0.000040" || got.CacheAmount != "0.000020" || got.ReferenceAmount != "0.000160" {
		t.Fatalf("unexpected amounts: %+v", got)
	}
}

func TestRequestContextTokensKeepsNormalizedLanesSeparate(t *testing.T) {
	log := PagedLogRecord{
		PromptTokens:  sql.NullInt64{Valid: true, Int64: 2},
		CacheTokens:   75841,
		ContextTokens: 76596,
	}
	if got := RequestContextTokens(log); got != 76596 {
		t.Fatalf("context tokens=%d", got)
	}
	if got := log.PromptTokens.Int64; got != 2 {
		t.Fatalf("ordinary input tokens changed to %d", got)
	}
}

func TestFormatBusinessTimeUsesBillingLocation(t *testing.T) {
	unix := time.Date(2026, 6, 30, 16, 0, 0, 0, time.UTC).Unix()
	if got := FormatBusinessTime(unix); got != "2026-07-01 00:00:00" {
		t.Fatalf("formatted time=%q", got)
	}
}

func TestFillAnomalyAmountsUsesContextTokensForTier(t *testing.T) {
	at := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	log := PagedLogRecord{
		PromptTokens:     sql.NullInt64{Valid: true, Int64: 100},
		CompletionTokens: sql.NullInt64{Valid: true, Int64: 1},
		CacheTokens:      900,
		ContextTokens:    1000,
	}
	prices := []Price{
		{EffectiveFrom: at, TierFrom: 0, Input: "1", Output: "1", Cache: "1"},
		{EffectiveFrom: at, TierFrom: 1000, Input: "2", Output: "2", Cache: "2"},
	}
	var got AnomalyOrder
	fillAnomalyAmounts(&got, log, prices, "1", at, true)
	if got.InputPrice != "2.000000" || got.CachePrice != "2.000000" {
		t.Fatalf("anomaly selected the wrong context tier: %+v", got)
	}
}

func TestFillAnomalyAmountsUsesNumericZeroWithoutPrice(t *testing.T) {
	var got AnomalyOrder
	fillAnomalyAmounts(&got, PagedLogRecord{}, nil, "1", time.Now(), true)
	if got.InputPrice != "0" || got.OutputPrice != "0" || got.CachePrice != "0" || got.InputAmount != "0" || got.OutputAmount != "0" || got.CacheAmount != "0" || got.ReferenceAmount != "0" {
		t.Fatalf("unpriced anomaly contains non-numeric defaults: %+v", got)
	}
}
