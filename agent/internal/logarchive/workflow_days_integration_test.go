package logarchive

import (
	af "controltower/internal/archivecontract"
	"strings"
	"testing"
	"time"
)

func TestWorkflowDayPagesMySQL(t *testing.T) {
	w, ctx, g, _ := scanFixture(t)
	if err := ensureMonthlyTables(ctx, w.target, nil); err != nil {
		t.Fatal(err)
	}
	state := workflowState{TaskID: strings.Repeat("d", 32), WorkflowStatus: af.WorkflowStatus{Phase: "reset_daily"}}
	if err := w.AcquireWriter(ctx, g, 90*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := w.commitWorkflow(ctx, g, state); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 102; i++ {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		if _, err := w.target.ExecContext(ctx, `INSERT INTO log_daily_stats(period_key,log_rows,request_rows,error_rows) VALUES(?,9007199254740993,10,2)`, date); err != nil {
			t.Fatal(err)
		}
	}
	// Daily visibility must work without touching the source connection.
	reader := &Worker{target: w.target}
	p, err := reader.WorkflowDayPage(ctx, "")
	if err != nil || len(p.Days) != 100 || p.NextAfter == "" || p.Days[0].Counts != nil || p.Days[0].State != "preparing" {
		t.Fatalf("cleanup page: %+v %v", p, err)
	}
	last, err := w.WorkflowDayPage(ctx, p.NextAfter)
	if err != nil || len(last.Days) != 2 || last.NextAfter != "" {
		t.Fatalf("tail page: %+v %v", last, err)
	}
	state.Phase = "import_target"
	if err = w.commitWorkflow(ctx, g, state); err != nil {
		t.Fatal(err)
	}
	p, err = w.WorkflowDayPage(ctx, "")
	if err != nil || p.Days[0].State != "rebuilding" || p.Days[0].Counts.LogRows != "9007199254740993" {
		t.Fatalf("import page: %+v %v", p, err)
	}
	for _, sql := range []string{
		`INSERT INTO archive_days(log_date,state,mutation_revision,updated_at) VALUES('2026-01-01','dirty',1,UTC_TIMESTAMP(6)),('2026-01-02','dirty',2,UTC_TIMESTAMP(6))`,
		`INSERT INTO archive_workflow_days(log_date,state,revision,config_version,error_code,updated_at) VALUES('2026-01-01','blocked',1,1,'source_history_unconfirmed',UTC_TIMESTAMP(6)),('2026-01-02','sealed',1,1,'',UTC_TIMESTAMP(6))`,
	} {
		if _, err = w.target.ExecContext(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	state.Phase, state.Date = "verify", "2026-01-03"
	if err = w.commitWorkflow(ctx, g, state); err != nil {
		t.Fatal(err)
	}
	p, err = w.WorkflowDayPage(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Days[0].State != "blocked" || p.Days[0].ErrorCode != "source_history_unconfirmed" || p.Days[0].UpdatedAt == nil {
		t.Fatalf("blocked: %+v", p.Days[0])
	}
	if p.Days[1].State != "pending" || p.Days[1].UpdatedAt != nil {
		t.Fatalf("stale seal trusted: %+v", p.Days[1])
	}
	if p.Days[2].State != "verify" {
		t.Fatalf("active day: %+v", p.Days[2])
	}
	stored, err := loadWorkflow(ctx, w.target)
	if err != nil || stored.Date != state.Date || stored.Phase != state.Phase || stored.AfterID != 0 {
		t.Fatal("daily view changed workflow", stored, err)
	}
}
