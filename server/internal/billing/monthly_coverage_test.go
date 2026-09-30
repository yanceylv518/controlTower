package billing

import (
	"reflect"
	"testing"
	"time"
)

func TestMonthlyCoverageDoesNotBridgeMissingDates(t *testing.T) {
	from := time.Date(2024, 2, 1, 0, 0, 0, 0, BusinessLocation)
	days := map[string]bool{"2024-02-02": false, "2024-02-03": false, "2024-02-05": false, "2024-03-01": true}
	c := NewMonthlyCoverage(from.UTC(), from.AddDate(0, 1, 0).UTC(), days)
	if c.Complete || c.TotalDays != 29 || c.CoveredDays != 3 || c.EmptyDays != 0 || !reflect.DeepEqual(c.Ranges, []CoverageRange{{"2024-02-02", "2024-02-03"}, {"2024-02-05", "2024-02-05"}}) || !reflect.DeepEqual(c.Missing, []CoverageRange{{"2024-02-01", "2024-02-01"}, {"2024-02-04", "2024-02-04"}, {"2024-02-06", "2024-02-29"}}) {
		t.Fatalf("incorrect coverage: %+v", c)
	}
	for day := from; day.Before(from.AddDate(0, 1, 0)); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		if _, exists := days[key]; !exists {
			days[key] = true
		}
	}
	c = NewMonthlyCoverage(from, from.AddDate(0, 1, 0), days)
	if !c.Complete || c.CoveredDays != 29 || c.EmptyDays != 26 || len(c.Missing) != 0 || c.RangeLabel() != "2024-02-01 至 2024-02-29" {
		t.Fatal(c)
	}
}

func TestMonthlyCoverageUnknownAndFrozenEvidence(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, BusinessLocation)
	job := Job{From: from, To: from.AddDate(0, 1, 0)}
	c := MonthlyCoverageFromRows(job, nil)
	if c.Complete || c.CoveredDays != 0 || c.RangeLabel() != "覆盖日期未确认" {
		t.Fatal(c)
	}
	rows := []StatementAggregateRow{{AggregateRow: AggregateRow{Day: from.AddDate(0, 0, 8), Amount: "0"}}}
	c = MonthlyCoverageFromRows(job, rows)
	if c.Complete || c.CoveredDays != 1 || c.EmptyDays != 0 || c.Ranges[0].From != "2026-09-09" {
		t.Fatal(c)
	}
	job.MonthlyCoverage = c
	if MonthlyCoverageFromRows(job, nil) != c {
		t.Fatal("saved coverage replaced by current query rows")
	}
}
