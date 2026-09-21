package logarchive

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	af "controltower/internal/archivecontract"
)

func TestCopyEvidenceTargetOnlyMySQL(t *testing.T) {
	for _, mode := range []string{"matched", "missing", "different", "extra", "empty"} {
		t.Run(mode, func(t *testing.T) {
			w, ctx, g, verify, scan := reconcileV4Fixture(t)
			from, _, _ := af.DateBounds(scan.Date)
			if mode != "empty" {
				scanRow(t, w, ctx, 1, from+1, `{"model_ratio":1}`)
			}
			for i := 0; i < 5; i++ {
				s, err := w.scanDate(ctx, g, scan, true)
				if err != nil {
					t.Fatal(err)
				}
				if s.State == "succeeded" {
					break
				}
			}
			table := "logs_" + strings.ReplaceAll(scan.Date[:7], "-", "")
			switch mode {
			case "missing":
				if _, err := w.target.ExecContext(ctx, "DELETE FROM "+table+" WHERE id=1"); err != nil {
					t.Fatal(err)
				}
			case "different":
				if _, err := w.target.ExecContext(ctx, "UPDATE "+table+" SET quota=999 WHERE id=1"); err != nil {
					t.Fatal(err)
				}
			case "extra":
				if _, err := w.target.ExecContext(ctx, "INSERT INTO "+table+"(id,created_at,type,quota,prompt_tokens,completion_tokens) VALUES(2,?,2,1,1,1)", from+2); err != nil {
					t.Fatal(err)
				}
			}
			// Any accidental source access now fails. Captured evidence must suffice.
			if err := w.source.Close(); err != nil {
				t.Fatal(err)
			}
			var result af.ReconcileStatus
			for i := 0; i < 10; i++ {
				var err error
				result, err = w.reconcileCopiedDate(ctx, g, verify, scan)
				if err != nil {
					t.Fatal(err)
				}
				if result.State != "running" {
					break
				}
			}
			want := "mismatched"
			if mode == "matched" || mode == "empty" {
				want = "matched"
			}
			if result.State != want || result.Method != copyReconcileMethod {
				t.Fatalf("got %+v", result)
			}
			if want == "mismatched" && result.IssueCount != 1 {
				t.Fatalf("missing row-level issue: %+v", result)
			}
		})
	}
}

func TestCopyEvidenceAtomicReplayMySQL(t *testing.T) {
	w, ctx, g, scan := scanFixture(t)
	b := reviewWriterBatch()
	scan.Date = writerTestDate(t, b)
	b.Scan = &scan
	b.Capture = true
	b.AfterCreated = 1788278400
	b.SourceNow = time.Now().Unix()
	if err := w.initializeScan(ctx, g, scan); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("before commit")
	if _, err := w.commitWriterBatch(ctx, g, b, writerFaultHooks{BeforeCommit: func(*sql.Tx) error { return injected }}); !errors.Is(err, injected) {
		t.Fatalf("fault: %v", err)
	}
	for _, table := range []string{"archive_scan_evidence", "archive_reconcile_issues", "archive_log_state", "archive_batch_receipts"} {
		if n := writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM "+table); n != 0 {
			t.Fatalf("partial commit in %s", table)
		}
	}
	if _, err := w.commitWriterBatch(ctx, g, b, writerFaultHooks{}); err != nil {
		t.Fatal(err)
	}
	if r, err := w.commitWriterBatch(ctx, g, b, writerFaultHooks{}); err != nil || !r.Replayed {
		t.Fatalf("replay %+v %v", r, err)
	}
	if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_reconcile_issues`); n != 1 {
		t.Fatal("evidence duplicated")
	}
}

func TestWorkflowClosingPassCapturesLateInsertMySQL(t *testing.T) {
	w, ctx, g, _ := scanFixture(t)
	day := time.Now().In(archiveLocation).AddDate(0, 0, -1).Format("2006-01-02")
	from, _, _ := af.DateBounds(day)
	scanRow(t, w, ctx, 20, from+100, `{"model_ratio":1}`)
	w.delay = 24 * time.Hour
	for i := 0; i < 20; i++ {
		if _, err := w.WorkflowPass(ctx, g, 90*time.Second, true); err != nil {
			t.Fatal(err)
		}
		if writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_log_state WHERE id=20`) > 0 {
			break
		}
	}
	// Behind both the timestamp and ID cursor; a tail-only scan would miss it.
	scanRow(t, w, ctx, 5, from+50, `{"model_ratio":1}`)
	w.delay = 5 * time.Minute
	for i := 0; i < 100; i++ {
		s, err := w.WorkflowPass(ctx, g, 90*time.Second, true)
		if err != nil {
			t.Fatalf("%+v %v", s, err)
		}
		if s.CompletedDays == 1 {
			if n := writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_log_state WHERE id IN (5,20)`); n != 2 {
				t.Fatal("late insert omitted")
			}
			return
		}
	}
	t.Fatal("closing pass did not seal")
}
