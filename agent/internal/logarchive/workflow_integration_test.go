package logarchive

import (
	af "controltower/internal/archivecontract"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestWholeArchiveWorkflowMySQL(t *testing.T) {
	for _, mode := range []string{"normal", "orphan", "crossday"} {
		t.Run(fmt.Sprintf("legacy_%s", mode), func(t *testing.T) {
			orphan := mode == "orphan"
			w, ctx, g, _ := scanFixture(t)
			day := time.Now().In(archiveLocation).AddDate(0, 0, -1).Format("2006-01-02")
			if mode == "crossday" {
				day = time.Now().In(archiveLocation).AddDate(0, 0, -2).Format("2006-01-02")
			}
			from, _, _ := af.DateBounds(day)
			month := strings.ReplaceAll(day[:7], "-", "")
			for id := int64(1); id <= 3; id++ {
				created := from + id
				if mode == "crossday" && id > 1 {
					created += 86400
				}
				scanRow(t, w, ctx, id, created, `{"model_ratio":1}`)
			}
			if err := ensureMonthlyTables(ctx, w.target, []string{month}); err != nil {
				t.Fatal(err)
			}
			if _, err := w.target.ExecContext(ctx, `DELETE FROM archive_checkpoints`); err != nil {
				t.Fatal(err)
			}
			for id := int64(1); id <= 2; id++ {
				quota := 50
				if id == 2 {
					quota = 99
				}
				if _, err := w.target.ExecContext(ctx, "INSERT INTO logs_"+month+"(id,created_at,type,quota,other,prompt_tokens,completion_tokens) VALUES(?,?,2,?, ?,5,10)", id, from+id, quota, `{"model_ratio":1}`); err != nil {
					t.Fatal(err)
				}
			}
			if orphan {
				if _, err := w.target.ExecContext(ctx, "INSERT INTO logs_"+month+"(id,created_at,type,quota,other,prompt_tokens,completion_tokens) VALUES(4,?,2,50,'orphan',5,10)", from+4); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := w.target.ExecContext(ctx, `INSERT INTO log_daily_stats(period_key,log_rows,quota) VALUES(?,999,999)`, day); err != nil {
				t.Fatal(err)
			}
			if _, err := w.target.ExecContext(ctx, `CREATE TABLE workflow_updates(id BIGINT PRIMARY KEY,n INT NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			if _, err := w.target.ExecContext(ctx, "CREATE TRIGGER workflow_raw_update AFTER UPDATE ON logs_"+month+" FOR EACH ROW INSERT INTO workflow_updates VALUES(NEW.id,1) ON DUPLICATE KEY UPDATE n=n+1"); err != nil {
				t.Fatal(err)
			}
			var status *af.WorkflowStatus
			for i := 0; i < 160; i++ {
				if i == 6 { // Restart and a new writer epoch must retain import progress.
					w = &Worker{source: w.source, target: w.target, batchSize: 1, delay: 5 * time.Minute}
					g.WriterEpoch++
					g.Session = strings.Repeat("8", 32)
				}
				w.SetBatchSize(1)
				var err error
				status, err = w.WorkflowPass(ctx, g, 90*time.Second, true)
				if err != nil {
					t.Fatalf("turn %d status=%+v error=%v", i, status, err)
				}
				// A forward date correction is discovered when its new day is read.
				// The older day may temporarily mismatch, then be retried as dirty.
				if status != nil && ((mode == "crossday" && status.CompletedDays == 2) || (mode != "crossday" && status.CompletedDays+status.BlockedDays > 0)) {
					break
				}
			}
			days := uint64(1)
			if mode == "crossday" {
				days = 2
			}
			if status == nil || status.CompletedDays+status.BlockedDays != days {
				t.Fatalf("did not finish: %+v", status)
			}
			if orphan && status.BlockedDays != 1 || !orphan && status.CompletedDays != days {
				t.Fatalf("unexpected completion: %+v", status)
			}
			want := int64(3)
			if orphan {
				want++
			}
			if n := writerTestScalar(t, ctx, w.target, "SELECT SUM(log_rows) FROM log_daily_stats"); n != want {
				t.Fatalf("double counted statistics=%d", n)
			}
			if n := writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM logs_"+month); n != want {
				t.Fatalf("raw rows=%d", n)
			}
			if n := writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM workflow_updates WHERE id=1"); n != 0 {
				t.Fatal("identical raw row rewritten")
			}
			if n := writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_raw_repairs WHERE source_id=2"); n < 1 {
				t.Fatal("changed legacy row repaired without audit")
			}
		})
	}
}

func TestWorkflowDeclarationAndExplicitRepairMySQL(t *testing.T) {
	w, ctx, g, _ := scanFixture(t)
	day := time.Now().In(archiveLocation).AddDate(0, 0, -1).Format("2006-01-02")
	from, _, _ := af.DateBounds(day)
	scanRow(t, w, ctx, 1, from+1, `{"model_ratio":1}`)
	run := func(immutable bool, wantSealed bool) {
		t.Helper()
		for i := 0; i < 100; i++ {
			status, err := w.WorkflowPass(ctx, g, 90*time.Second, immutable)
			if err != nil {
				t.Fatalf("pass=%+v err=%v", status, err)
			}
			if status != nil && status.Phase == "live" && ((wantSealed && status.CompletedDays == 1 && status.BlockedDays == 0) || (!wantSealed && status.BlockedDays == 1)) {
				return
			}
		}
		t.Fatal("workflow failed to reach expected terminal day state")
	}
	run(false, false)
	if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_day_versions`); n != 0 {
		t.Fatal("unconfirmed history sealed")
	}
	g.ConfigVersion++
	run(true, true)
	var firstVersion string
	if err := w.target.QueryRowContext(ctx, `SELECT HEX(current_version_id) FROM archive_days WHERE log_date=?`, day).Scan(&firstVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := w.source.ExecContext(ctx, `UPDATE logs SET quota=80 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.target.ExecContext(ctx, `UPDATE archive_workflow_days SET updated_at=UTC_TIMESTAMP()-INTERVAL 25 HOUR`); err != nil {
		t.Fatal(err)
	}
	// Unchanged sealed dates are no longer periodically re-read.
	for i := 0; i < 6; i++ {
		if _, err := w.WorkflowPass(ctx, g, 90*time.Second, true); err != nil {
			t.Fatal(err)
		}
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT quota FROM log_daily_stats WHERE period_key=?`, day); n != 50 {
		t.Fatal("sealed day was periodically rescanned")
	}
	// An explicit target invalidation still requests repair and new verification.
	if _, err := w.target.ExecContext(ctx, `UPDATE archive_days SET mutation_revision=mutation_revision+1,state='dirty' WHERE log_date=?`, day); err != nil {
		t.Fatal(err)
	}
	if _, err := w.WorkflowPass(ctx, g, 90*time.Second, true); err != nil {
		t.Fatal(err)
	}
	run(true, true)
	if n := writerTestScalar(t, ctx, w.target, `SELECT quota FROM log_daily_stats WHERE period_key=?`, day); n != 80 {
		t.Fatalf("late low-ID change missed: quota=%d", n)
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_day_versions WHERE HEX(day_version_id)=? AND state='published'`, firstVersion); n != 1 {
		t.Fatal("old version replaced")
	}
}

// IDs deliberately disagree with time order. A hidden incremental scan would
// copy tomorrow before the older day has been handled and create ID receipts.
func TestWorkflowChronologicalMySQL(t *testing.T) {
	w, ctx, g, _ := scanFixture(t)
	day := time.Now().In(archiveLocation).AddDate(0, 0, -2).Format("2006-01-02")
	from, _, _ := af.DateBounds(day)
	scanRow(t, w, ctx, 30, from+1, `{"model_ratio":1}`)
	scanRow(t, w, ctx, 20, from+86401, `{"model_ratio":1}`)
	current := time.Now().Add(-20 * time.Minute).Unix()
	scanRow(t, w, ctx, 10, current, `{"model_ratio":1}`)
	w.SetBatchSize(1)
	for i := 0; i < 300; i++ {
		status, err := w.WorkflowPass(ctx, g, 90*time.Second, true)
		if err != nil {
			t.Fatalf("turn %d: %v (%+v)", i, err, status)
		}
		if status.CompletedDays == 0 && writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_log_state WHERE id IN (10,20)`) > 0 {
			t.Fatal("read ahead before first date completed")
		}
		if writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_log_state WHERE id=10`) > 0 {
			break
		}
		if i == 299 {
			t.Fatal("never reached current date")
		}
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_batch_receipts WHERE stream_key='incremental'`); n != 0 {
		t.Fatalf("unexpected ID scan receipts=%d", n)
	}
	// Restart with an older ID but later timestamp on the open date.
	w = &Worker{source: w.source, target: w.target, batchSize: 1, delay: 5 * time.Minute}
	g.WriterEpoch++
	g.Session = strings.Repeat("9", 32)
	scanRow(t, w, ctx, 5, current+1, `{"model_ratio":1}`)
	for i := 0; i < 20; i++ {
		if _, err := w.WorkflowPass(ctx, g, 90*time.Second, true); err != nil {
			t.Fatal(err)
		}
		if writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_log_state WHERE id=5`) > 0 {
			break
		}
		if i == 19 {
			t.Fatal("open date did not resume chronological cursor")
		}
	}
	today := time.Unix(current, 0).In(archiveLocation).Format("2006-01-02")
	if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_day_versions WHERE log_date=?`, today); n != 0 {
		t.Fatal("open date sealed")
	}
}

func TestWorkflowOldSourceScanTransitionMySQL(t *testing.T) {
	w, ctx, g, _ := scanFixture(t)
	if err := w.AcquireWorkflow(ctx, g, 90*time.Second); err != nil {
		t.Fatal(err)
	}
	s, err := loadWorkflow(ctx, w.target)
	if err != nil {
		t.Fatal(err)
	}
	s.Phase = "source_scan"
	if err = w.commitWorkflow(ctx, g, s); err != nil {
		t.Fatal(err)
	}
	status, err := w.WorkflowPass(ctx, g, 90*time.Second, true)
	if err != nil || status.Phase != "live" {
		t.Fatalf("transition: %+v %v", status, err)
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_batch_receipts WHERE stream_key='incremental'`); n != 0 {
		t.Fatal("legacy phase read incremental history")
	}
}

func TestWorkflowOpenDateClosesWithoutRewindMySQL(t *testing.T) {
	w, ctx, g, task := scanFixture(t)
	day := time.Now().In(archiveLocation).AddDate(0, 0, -1).Format("2006-01-02")
	from, _, _ := af.DateBounds(day)
	task.Date = day
	task.Policy.CoverageFrom = day
	task.Policy.SourceRetainedFrom = day
	scanRow(t, w, ctx, 1, from+1, `{"model_ratio":1}`)
	w.delay = 24 * time.Hour
	status, err := w.scanDate(ctx, g, task, true)
	if err != nil || status.State != "running" || status.ScannedRows != 1 {
		t.Fatalf("open: %+v %v", status, err)
	}
	before := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_batch_receipts`)
	if _, err = w.scanDate(ctx, g, task, true); err != nil {
		t.Fatal(err)
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_batch_receipts`); n != before {
		t.Fatal("idle open day wrote a new receipt")
	}
	w.delay = 5 * time.Minute
	status, err = w.scanDate(ctx, g, task, true)
	if err != nil || status.State != "succeeded" || status.ScannedRows != 1 {
		t.Fatalf("close without rewind: %+v %v", status, err)
	}
}
