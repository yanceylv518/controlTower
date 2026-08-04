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
		{"unknown context accepted", PagedLogRecord{PromptTokens: sql.NullInt64{Valid: true, Int64: 1000000}, CompletionTokens: sql.NullInt64{Valid: true, Int64: 1}}, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ""
			for i, v := range anomalyReasons(tc.log, tc.max) {
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
	if got.InputAmount != "0.000060" || got.OutputAmount != "0.000040" || got.CacheAmount != "0.000020" || got.ReferenceAmount != "0.000120" {
		t.Fatalf("unexpected amounts: %+v", got)
	}
}
