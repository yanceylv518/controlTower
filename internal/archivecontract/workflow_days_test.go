package archivecontract

import (
	"strings"
	"testing"
	"time"
)

func TestWorkflowDayPageValidation(t *testing.T) {
	now := time.Now().UTC()
	page := WorkflowDayPage{TaskID: strings.Repeat("a", 32), ObservedAt: now, Days: []WorkflowDay{{Date: "2026-09-01", State: "rebuilding", ObservedAt: now, Counts: &WorkflowDayCounts{LogRows: "9007199254740993", RequestRows: "0", ErrorRows: "0"}}}}
	if err := page.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*WorkflowDayPage){
		func(p *WorkflowDayPage) { p.Days[0].Date = "2026-02-30" },
		func(p *WorkflowDayPage) {
			p.Days[0].Counts = &WorkflowDayCounts{LogRows: "-1", RequestRows: "0", ErrorRows: "0"}
		},
		func(p *WorkflowDayPage) { p.Days = append(p.Days, p.Days[0]) },
		func(p *WorkflowDayPage) { p.NextAfter = "2026-09-02" },
		func(p *WorkflowDayPage) { p.Days[0].State = "complete" },
		func(p *WorkflowDayPage) { p.Days[0].ObservedAt = now.Add(time.Second) },
	} {
		copy := page
		copy.Days = append([]WorkflowDay(nil), page.Days...)
		change(&copy)
		if copy.Validate() == nil {
			t.Fatalf("accepted invalid page: %+v", copy)
		}
	}
}
