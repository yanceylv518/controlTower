package logarchive

import (
	af "controltower/internal/archivecontract"
	"strings"
	"testing"
	"time"
)

func TestWorkflowPreparationProgressMySQL(t *testing.T) {
	w, ctx, g, _ := scanFixture(t)
	for _, stmt := range []string{
		`DELETE FROM archive_checkpoints`,
		`INSERT INTO archive_log_state(id,contribution) VALUES(1,'{}'),(2,'{}'),(3,'{}'),(4,'{}'),(5,'{}')`,
	} {
		if _, err := w.target.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := ensureMonthlyTables(ctx, w.target, []string{"202609"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.target.ExecContext(ctx, `INSERT INTO logs_202609(id,created_at,type,quota,other,prompt_tokens,completion_tokens) VALUES(1,1788278400,2,50,'{}',5,10),(2,1788278401,2,50,'{}',5,10)`); err != nil {
		t.Fatal(err)
	}
	w.SetBatchSize(2)
	first, err := w.WorkflowPass(ctx, g, 90*time.Second, false)
	if err != nil || first == nil || first.Preparation == nil {
		t.Fatalf("first page: %+v %v", first, err)
	}
	p := *first.Preparation
	if first.Phase != "reset_state" || p.ProcessedRows != 2 || p.LastBatchRows != 2 || p.CommittedBatches != 1 || p.Table != "archive_log_state" || p.Validate() != nil {
		t.Fatalf("first page: %+v", p)
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_log_state`); n != 3 {
		t.Fatalf("remaining rows: %d", n)
	}
	// Failure to persist the progress must also roll back the actual DELETE.
	if _, err = w.target.ExecContext(ctx, `CREATE TRIGGER reject_workflow_progress BEFORE UPDATE ON archive_workflow FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='test rollback'`); err != nil {
		t.Fatal(err)
	}
	failed, err := w.WorkflowPass(ctx, g, 90*time.Second, false)
	if err == nil || failed == nil || failed.Preparation.ProcessedRows != 2 || !failed.Preparation.LastCommittedAt.Equal(p.LastCommittedAt) {
		t.Fatalf("failed transaction advanced progress: %+v %v", failed, err)
	}
	if diagnostic := Diagnose(err); diagnostic.MySQLNumber != 1644 || diagnostic.SQLState != "45000" {
		t.Fatalf("database cause lost: %+v", diagnostic)
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_log_state`); n != 3 {
		t.Fatalf("failed transaction deleted rows: %d", n)
	}
	if _, err = w.target.ExecContext(ctx, `DROP TRIGGER reject_workflow_progress`); err != nil {
		t.Fatal(err)
	}
	// A new process/session resumes committed counters, rather than starting over.
	w = &Worker{source: w.source, target: w.target, batchSize: 2, delay: 5 * time.Minute}
	g.WriterEpoch++
	g.Session = strings.Repeat("9", 32)
	second, err := w.WorkflowPass(ctx, g, 90*time.Second, false)
	if err != nil || second.Preparation.ProcessedRows != 4 || second.Preparation.CommittedBatches != 2 || !second.Preparation.RecordedSince.Equal(p.RecordedSince) {
		t.Fatalf("restart: %+v %v", second, err)
	}
	for i := 0; i < 12; i++ {
		status, err := w.WorkflowPass(ctx, g, 90*time.Second, false)
		if err != nil {
			t.Fatal(err)
		}
		if status.Phase == "reset_daily" && (status.Preparation.Phase != "reset_state" || status.Preparation.ProcessedRows != 5 || status.Preparation.LastBatchRows != 0) {
			t.Fatalf("phase boundary lost final cleanup result: %+v", status.Preparation)
		}
		if status.Phase == "live" {
			if status.ImportedRows != 2 || status.Preparation.Phase != "import_target" || status.Preparation.ProcessedRows != 2 {
				t.Fatalf("import counts: %+v", status)
			}
			break
		}
		if i == 11 {
			t.Fatal("preparation did not finish")
		}
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM logs_202609`); n != 2 {
		t.Fatalf("raw archive changed: %d", n)
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_log_state`); n != 2 {
		t.Fatalf("contributions not rebuilt: %d", n)
	}
}

func TestPreparationProgressDoesNotInventPreUpgradeCounts(t *testing.T) {
	s := workflowState{WorkflowStatus: af.WorkflowStatus{Phase: "reset_state"}}
	now := time.Now().UTC()
	recordPreparationProgress(&s, "reset_state", "archive_log_state", 0, 1000, now)
	if s.Preparation.ProcessedRows != 1000 || !s.Preparation.RecordedSince.Equal(now) {
		t.Fatalf("invented historical count: %+v", s.Preparation)
	}
	recordPreparationProgress(&s, "reset_daily", "log_daily_stats", 0, 3, now.Add(time.Second))
	if s.Preparation.ProcessedRows != 3 || s.Preparation.CommittedBatches != 1 {
		t.Fatalf("mixed phase counters: %+v", s.Preparation)
	}
}
