package logarchive

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	af "controltower/internal/archivecontract"
)

func reconcileV4Fixture(t *testing.T) (*Worker, context.Context, af.WriterGrant, af.ReconcileTask, af.BackfillTask) {
	t.Helper()
	w, ctx, g, b := scanFixture(t)
	_, end, _ := af.DateBounds(b.Date)
	r := af.ReconcileTask{Identity: b.Identity, TaskID: strings.Repeat("c", 32), Date: b.Date, Attempt: 1, Policy: b.Policy, Assurance: af.VerificationAssurance{StableBeforeUnix: end, ValidUntilUnix: time.Now().Add(time.Hour).Unix(), Evidence: "isolated source retained and immutable during test"}}
	return w, ctx, g, r, b
}

func completeReconcileV4(t *testing.T, w *Worker, ctx context.Context, g af.WriterGrant, r af.ReconcileTask) af.ReconcileStatus {
	t.Helper()
	for i := 0; i < 100; i++ {
		s, err := w.ReconcileDateV4(ctx, g, r)
		if err != nil {
			t.Fatal(err)
		}
		if s.Validate() != nil {
			t.Fatalf("invalid status: %+v", s)
		}
		if s.State != "running" {
			return s
		}
	}
	t.Fatal("reconciliation never finished")
	return af.ReconcileStatus{}
}

func TestReconcileV4MySQL(t *testing.T) {
	t.Run("histogram_json_database_round_trip_above_javascript_precision", func(t *testing.T) {
		w, ctx := foundationTestWorker(t)
		state := newReconcileScans()
		state.Scans[0].Summary.Types["2"] = 9007199254740993
		state.Scans[0].Summary.Nulls["other"] = ^uint64(0)
		raw, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.target.ExecContext(ctx, "CREATE TABLE histogram_checkpoint_fixture (checkpoint JSON NOT NULL) ENGINE=InnoDB"); err != nil {
			t.Fatal(err)
		}
		if _, err := w.target.ExecContext(ctx, "INSERT INTO histogram_checkpoint_fixture(checkpoint) VALUES(?)", string(raw)); err != nil {
			t.Fatal(err)
		}
		var persisted []byte
		if err := w.target.QueryRowContext(ctx, "SELECT checkpoint FROM histogram_checkpoint_fixture").Scan(&persisted); err != nil {
			t.Fatal(err)
		}
		var restored reconcileScans
		if err := json.Unmarshal(persisted, &restored); err != nil || !restored.valid() || restored.Scans[0].Summary.Types["2"] != 9007199254740993 || restored.Scans[0].Summary.Nulls["other"] != ^uint64(0) {
			t.Fatalf("database checkpoint lost histogram precision: %+v %v", restored.Scans[0].Summary, err)
		}
		var kind string
		if err := w.target.QueryRowContext(ctx, `SELECT JSON_TYPE(JSON_EXTRACT(checkpoint,'$.scans[0].summary.types."2"')) FROM histogram_checkpoint_fixture`).Scan(&kind); err != nil || kind != "STRING" {
			t.Fatalf("database stored unsafe histogram value: %q %v", kind, err)
		}
	})
	t.Run("failed_terminal_commit_reports_only_committed_progress", func(t *testing.T) {
		w, ctx, g, r, b := reconcileV4Fixture(t)
		from, _, _ := af.DateBounds(r.Date)
		scanRow(t, w, ctx, 1, from+1, "original")
		if _, err := w.ScanDateV3(ctx, g, b); err != nil {
			t.Fatal(err)
		}
		var prior af.ReconcileStatus
		for i := 0; i < 3; i++ {
			var err error
			prior, err = w.ReconcileDateV4(ctx, g, r)
			if err != nil {
				t.Fatal(err)
			}
		}
		if prior.Phase != "target_second" || prior.State != "running" {
			t.Fatal(prior)
		}
		if _, err := w.target.ExecContext(ctx, `CREATE TRIGGER reject_reconcile_terminal BEFORE UPDATE ON archive_reconcile_runs FOR EACH ROW BEGIN IF NEW.state='matched' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected terminal commit failure'; END IF; END`); err != nil {
			t.Fatal(err)
		}
		failed, err := w.ReconcileDateV4(ctx, g, r)
		if err == nil || failed != prior {
			t.Fatalf("reported uncommitted terminal state: prior=%+v got=%+v err=%v", prior, failed, err)
		}
		e, err := w.ReadReconcileV4(ctx, g.Identity, prior.RunID)
		if err != nil || e.State != "running" || e.Phase != "target_second" {
			t.Fatalf("failure changed durable state: %+v %v", e, err)
		}
		if _, err := w.target.ExecContext(ctx, "DROP TRIGGER reject_reconcile_terminal"); err != nil {
			t.Fatal(err)
		}
		last := completeReconcileV4(t, w, ctx, g, r)
		if last.State != "matched" || last.RunID != prior.RunID {
			t.Fatalf("recovery failed: %+v", last)
		}
	})
	t.Run("matched_byte_exact_and_immutable_terminal_evidence", func(t *testing.T) {
		w, ctx, g, r, b := reconcileV4Fixture(t)
		from, _, _ := af.DateBounds(r.Date)
		scanRow(t, w, ctx, 1, from+1, "first")
		scanRow(t, w, ctx, 2, from+1, "")
		if _, err := w.source.ExecContext(ctx, "UPDATE logs SET quota=9223372036854775807,other=IF(id=1,NULL,other)"); err != nil {
			t.Fatal(err)
		}
		if _, err := w.ScanDateV3(ctx, g, b); err != nil {
			t.Fatal(err)
		}
		s := completeReconcileV4(t, w, ctx, g, r)
		if s.State != "matched" || s.SourceRows != 2 || s.StartRevision != s.FinalRevision {
			t.Fatalf("not matched: %+v", s)
		}
		e, err := w.ReadReconcileV4(ctx, g.Identity, s.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if e.SourceSummary.Quota != "18446744073709551614" || e.SourceSummary.Nulls["other"] != 1 || e.Method != "stable_window_paged" || e.Assurance != r.Assurance {
			t.Fatalf("incorrect evidence: %+v", e)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_day_versions") != 0 {
			t.Fatal("reconcile published version")
		}
		replayed, err := w.ReconcileDateV4(ctx, g, r)
		if err != nil || replayed != s {
			t.Fatalf("terminal run changed: %+v %v", replayed, err)
		}
		next := g
		next.WriterEpoch++
		next.Session = strings.Repeat("d", 32)
		if err := w.AcquireWriter(ctx, next, 90*time.Second); err != nil {
			t.Fatal(err)
		}
		replayed, err = w.ReconcileDateV4(ctx, next, r)
		if err != nil || replayed.WriterEpoch != next.WriterEpoch || replayed.RunID != s.RunID || replayed.ProgressVersion != s.ProgressVersion {
			t.Fatalf("terminal epoch replay: %+v %v", replayed, err)
		}
	})
	t.Run("missing_different_extra_rows_located_without_deleting", func(t *testing.T) {
		w, ctx, g, r, b := reconcileV4Fixture(t)
		from, _, _ := af.DateBounds(r.Date)
		scanRow(t, w, ctx, 1, from+1, "one")
		scanRow(t, w, ctx, 2, from+2, "two")
		if _, err := w.ScanDateV3(ctx, g, b); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"DELETE FROM logs_202609 WHERE id=2", "UPDATE logs_202609 SET other='different' WHERE id=1", "INSERT INTO logs_202609 SELECT 3,created_at,type,quota,other,prompt_tokens,completion_tokens FROM logs_202609 WHERE id=1"} {
			if _, err := w.target.ExecContext(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		s := completeReconcileV4(t, w, ctx, g, r)
		if s.State != "mismatched" || s.IssueCount != 3 {
			t.Fatalf("missing differences: %+v", s)
		}
		run, _ := af.IDBytes(s.RunID)
		for _, kind := range []string{"missing_target", "different", "extra_target"} {
			if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_reconcile_issues WHERE run_id=? AND issue_kind=?", run, kind) != 1 {
				t.Fatal("incorrect difference kind " + kind)
			}
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM logs_202609 WHERE id=3") != 1 {
			t.Fatal("target extra was deleted")
		}
	})
	t.Run("durable_pages_resume_with_new_epoch_and_large_id", func(t *testing.T) {
		w, ctx, g, r, b := reconcileV4Fixture(t)
		from, _, _ := af.DateBounds(r.Date)
		scanRow(t, w, ctx, 9007199254740993, from+1, "large")
		scanRow(t, w, ctx, 2, from+2, "small")
		if _, err := w.ScanDateV3(ctx, g, b); err != nil {
			t.Fatal(err)
		}
		w.SetBatchSize(1)
		first, err := w.ReconcileDateV4(ctx, g, r)
		if err != nil || first.SourceRows != 1 || first.Phase != "source_first" {
			t.Fatalf("first page: %+v %v", first, err)
		}
		restarted := &Worker{source: w.source, target: w.target, batchSize: 1, delay: w.delay}
		next := g
		next.WriterEpoch++
		next.Session = strings.Repeat("d", 32)
		if err := restarted.AcquireWriter(ctx, next, 90*time.Second); err != nil {
			t.Fatal(err)
		}
		if _, err := w.ReconcileDateV4(ctx, g, r); !errors.Is(err, ErrWriterLease) {
			t.Fatalf("old writer continued: %v", err)
		}
		last := completeReconcileV4(t, restarted, ctx, next, r)
		if last.State != "matched" || last.SourceRows != 2 || last.RunID != first.RunID {
			t.Fatalf("restarted run incorrect: %+v", last)
		}
		newAttempt := r
		newAttempt.Attempt++
		s, err := restarted.ReconcileDateV4(ctx, next, newAttempt)
		if err != nil || s.RunID == last.RunID {
			t.Fatalf("retry reused old evidence: %+v %v", s, err)
		}
		if _, err := restarted.ReconcileDateV4(ctx, next, r); !errors.Is(err, ErrWriterCheckpoint) {
			t.Fatalf("old attempt accepted: %v", err)
		}
	})
	t.Run("source_changes_between_scans_never_match", func(t *testing.T) {
		w, ctx, g, r, b := reconcileV4Fixture(t)
		from, _, _ := af.DateBounds(r.Date)
		scanRow(t, w, ctx, 1, from+1, "before")
		if _, err := w.ScanDateV3(ctx, g, b); err != nil {
			t.Fatal(err)
		}
		first, err := w.ReconcileDateV4(ctx, g, r)
		if err != nil || first.Phase != "target_first" {
			t.Fatalf("first scan: %+v %v", first, err)
		}
		if _, err := w.source.ExecContext(ctx, "UPDATE logs SET other='after'"); err != nil {
			t.Fatal(err)
		}
		s := completeReconcileV4(t, w, ctx, g, r)
		if s.State != "blocked" || s.ErrorCode != "source_drift" {
			t.Fatalf("unstable source accepted: %+v", s)
		}
	})
	t.Run("target_revision_change_blocks_entire_run", func(t *testing.T) {
		w, ctx, g, r, b := reconcileV4Fixture(t)
		from, _, _ := af.DateBounds(r.Date)
		scanRow(t, w, ctx, 1, from+1, "before")
		if _, err := w.ScanDateV3(ctx, g, b); err != nil {
			t.Fatal(err)
		}
		if _, err := w.ReconcileDateV4(ctx, g, r); err != nil {
			t.Fatal(err)
		}
		scanRow(t, w, ctx, 2, from+2, "late")
		if _, err := w.PassV2(ctx, g); err != nil {
			t.Fatal(err)
		}
		s := completeReconcileV4(t, w, ctx, g, r)
		if s.State != "blocked" || s.ErrorCode != "target_drift" {
			t.Fatalf("changed day accepted: %+v", s)
		}
	})
	for _, tc := range []struct {
		name, code string
		mutate     func(*testing.T, *Worker, context.Context, *af.ReconcileTask)
	}{
		{"unknown_history", "source_history_unknown", func(t *testing.T, w *Worker, ctx context.Context, r *af.ReconcileTask) {
			r.Policy.SourceRetainedFrom = ""
		}},
		{"cleared_history", "source_cleared", func(t *testing.T, w *Worker, ctx context.Context, r *af.ReconcileTask) {
			r.Policy.SourceRetainedFrom = "2026-09-02"
		}},
		{"expired_assurance", "verification_expired", func(t *testing.T, w *Worker, ctx context.Context, r *af.ReconcileTask) {
			r.Assurance.ValidUntilUnix = r.Assurance.StableBeforeUnix + 1
		}},
		{"missing_source_index", "source_index_missing", func(t *testing.T, w *Worker, ctx context.Context, r *af.ReconcileTask) {
			if _, err := w.source.ExecContext(ctx, "ALTER TABLE logs DROP INDEX idx_created"); err != nil {
				t.Fatal(err)
			}
		}},
		{"global_undated_issue", "global_blocked", func(t *testing.T, w *Worker, ctx context.Context, r *af.ReconcileTask) {
			if _, err := w.target.ExecContext(ctx, "UPDATE archive_dataset_meta SET unscoped_blocking_issues=1"); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, ctx, g, r, _ := reconcileV4Fixture(t)
			tc.mutate(t, w, ctx, &r)
			s := completeReconcileV4(t, w, ctx, g, r)
			if s.State != "blocked" || s.ErrorCode != tc.code {
				t.Fatalf("unsafe condition accepted: %+v", s)
			}
		})
	}
	t.Run("empty_day_requires_two_scans_and_remains_unsealed", func(t *testing.T) {
		w, ctx, g, r, _ := reconcileV4Fixture(t)
		s := completeReconcileV4(t, w, ctx, g, r)
		if s.State != "matched" || s.SourceRows != 0 || s.ProgressVersion != 5 {
			t.Fatalf("empty evidence: %+v", s)
		}
	})
	t.Run("corrupt_persistent_digest_cannot_resume", func(t *testing.T) {
		w, ctx, g, r, _ := reconcileV4Fixture(t)
		s, err := w.ReconcileDateV4(ctx, g, r)
		if err != nil {
			t.Fatal(err)
		}
		run, _ := af.IDBytes(s.RunID)
		if _, err := w.target.ExecContext(ctx, `UPDATE archive_reconcile_runs SET scan_json=JSON_SET(scan_json,'$.version',99) WHERE run_id=?`, run); err != nil {
			t.Fatal(err)
		}
		if _, err := w.ReconcileDateV4(ctx, g, r); !errors.Is(err, ErrWriterCheckpoint) {
			t.Fatalf("corrupt scan resumed: %v", err)
		}
	})
}
