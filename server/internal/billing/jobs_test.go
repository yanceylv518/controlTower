package billing

import (
	"database/sql"
	"testing"
	"time"
)

func TestNewJobSplitsRangeIntoDailySteps(t *testing.T) {
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, BusinessLocation)
	job, steps, err := NewJob("site-a", from, from.Add(25*time.Hour), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if job.TotalSteps != 2 || len(steps) != 2 {
		t.Fatalf("steps=%d total=%d", len(steps), job.TotalSteps)
	}
	if steps[0].To.Sub(steps[0].From) != 24*time.Hour || steps[1].To.Sub(steps[1].From) != time.Hour {
		t.Fatalf("steps=%#v", steps)
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
	from := time.Date(2026, 8, 1, 23, 30, 0, 0, BusinessLocation)
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
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, BusinessLocation)
	if _, _, err := NewJob("site-a", from, from.AddDate(0, 0, 61), "admin"); err == nil {
		t.Fatal("expected a range longer than 60 days to be rejected")
	}
	if _, steps, err := NewJob("site-a", from, from.AddDate(0, 0, 60), "admin"); err != nil || len(steps) != 60 {
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
		{"missing", PagedLogRecord{}, 100, "output_token_missing"},
		{"zero output remains in the complete bill", PagedLogRecord{PromptTokens: sql.NullInt64{Valid: true}, CompletionTokens: sql.NullInt64{Valid: true}}, 100, ""},
		{"negative output is not the zero-output filter", PagedLogRecord{PromptTokens: sql.NullInt64{Valid: true}, CompletionTokens: sql.NullInt64{Valid: true, Int64: -1}}, 100, ""},
		{"context does not make a billing anomaly", PagedLogRecord{PromptTokens: sql.NullInt64{Valid: true, Int64: 101}, CompletionTokens: sql.NullInt64{Valid: true, Int64: 1}}, 100, ""},
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

func TestStatementAnomalyReasonsUsesTaskZeroOutputPolicy(t *testing.T) {
	zero := PagedLogRecord{PromptTokens: sql.NullInt64{Valid: true}, CompletionTokens: sql.NullInt64{Valid: true}}
	if got := StatementAnomalyReasons(zero, 0, false); len(got) != 0 {
		t.Fatalf("complete statement excluded zero output: %v", got)
	}
	if got := StatementAnomalyReasons(zero, 0, true); len(got) != 1 || got[0] != "output_token_zero" {
		t.Fatalf("filtered statement did not retain zero output separately: %v", got)
	}
	negative := PagedLogRecord{CompletionTokens: sql.NullInt64{Valid: true, Int64: -1}}
	if got := StatementAnomalyReasons(negative, 0, true); len(got) != 0 {
		t.Fatalf("zero-output policy excluded a non-zero value: %v", got)
	}
}

func TestFillAnomalyChargeUsesReconstructedRequestAmounts(t *testing.T) {
	var got AnomalyOrder
	fillAnomalyCharge(&got, LogCharge{InputPrice: "1", OutputPrice: "2", CacheReadPrice: "0.5", InputAmount: "0.000100", OutputAmount: "0.000040", CacheReadAmount: "0.000020", Total: "0.000160"})
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

func TestFillAnomalyChargeCombinesCacheWriteDurations(t *testing.T) {
	var got AnomalyOrder
	fillAnomalyCharge(&got, LogCharge{CacheWrite5mPrice: "3.75", CacheWrite5mAmount: "0.001", CacheWrite1hAmount: "0.002", Total: "0.003"})
	if got.CacheWritePrice != "3.75" || got.CacheWriteAmount != "0.003000" || got.ReferenceAmount != "0.003" {
		t.Fatalf("unexpected cache-write charge: %+v", got)
	}
}

func TestFillAnomalyChargeUsesNumericZeroWithoutPrice(t *testing.T) {
	var got AnomalyOrder
	fillAnomalyCharge(&got, LogCharge{})
	if got.InputPrice != "0" || got.OutputPrice != "0" || got.CachePrice != "0" || got.InputAmount != "0" || got.OutputAmount != "0" || got.CacheAmount != "0" || got.ReferenceAmount != "0" {
		t.Fatalf("unpriced anomaly contains non-numeric defaults: %+v", got)
	}
}
