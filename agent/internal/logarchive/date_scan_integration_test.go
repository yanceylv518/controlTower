package logarchive

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	af "controltower/internal/archivecontract"
)

func scanFixture(t *testing.T) (*Worker, context.Context, af.WriterGrant, af.BackfillTask) {
	t.Helper()
	w, ctx, grant := reviewWriterFixture(t)
	if _, err := w.source.ExecContext(ctx, `ALTER TABLE logs ADD INDEX idx_created(created_at)`); err != nil {
		t.Fatal(err)
	}
	policy := af.DefaultCoveragePolicy()
	policy.CoverageFrom = "2026-08-01"
	policy.SourceRetainedFrom = "2026-08-01"
	policy.Evidence = "isolated fixture retains all rows"
	policy.Revision = 1
	task := af.BackfillTask{Identity: grant.Identity, TaskID: strings.Repeat("a", 32), Date: "2026-09-01", Type: "date_backfill", Attempt: 1, Policy: policy}
	return w, ctx, grant, task
}

func scanRow(t *testing.T, w *Worker, ctx context.Context, id, created int64, other string) {
	t.Helper()
	if _, err := w.source.ExecContext(ctx, `INSERT INTO logs(id,created_at,type,quota,other,prompt_tokens,completion_tokens) VALUES(?,?,2,50,?,5,10)`, id, created, other); err != nil {
		t.Fatal(err)
	}
}

func TestDateScanMySQL(t *testing.T) {
	t.Run("time_order_restart_epoch_and_independent_cursor", func(t *testing.T) {
		w, ctx, grant, task := scanFixture(t)
		from, _, _ := af.DateBounds(task.Date)
		scanRow(t, w, ctx, 90, from+1, "first")
		scanRow(t, w, ctx, 2, from+2, "second")
		scanRow(t, w, ctx, 7, from+2, "third")
		w.SetBatchSize(1)
		first, err := w.ScanDateV3(ctx, grant, task)
		if err != nil || first.ScannedRows != 1 || first.AfterID != 90 || first.State != "running" {
			t.Fatalf("first page: %+v %v", first, err)
		}
		if main, err := w.ProgressV2(ctx, grant.Identity); err != nil || main.AfterID != 0 || main.BatchID != "" {
			t.Fatalf("scan changed main cursor: %+v %v", main, err)
		}
		restarted := &Worker{source: w.source, target: w.target, batchSize: 1, delay: w.delay}
		next := grant
		next.WriterEpoch++
		next.Session = strings.Repeat("b", 32)
		if err := restarted.AcquireWriter(ctx, next, 90*time.Second); err != nil {
			t.Fatal(err)
		}
		second, err := restarted.ScanDateV3(ctx, next, task)
		if err != nil || second.AfterID != 2 || second.ScannedRows != 2 {
			t.Fatalf("restart timestamp cursor: %+v %v", second, err)
		}
		last, err := restarted.ScanDateV3(ctx, next, task)
		if err != nil || last.State != "succeeded" || last.ScannedRows != 3 || last.AfterID != 7 || last.Validate() != nil {
			t.Fatalf("completed: %+v %v", last, err)
		}
		replay, err := restarted.ScanDateV3(ctx, next, task)
		if err != nil || replay.BatchID != last.BatchID || replay.ScannedRows != 3 {
			t.Fatalf("terminal replay: %+v %v", replay, err)
		}
		if _, err := w.ScanDateV3(ctx, grant, task); !errors.Is(err, ErrWriterLease) {
			t.Fatalf("old epoch resumed scan: %v", err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_day_versions") != 0 {
			t.Fatal("scan published a version")
		}
	})
	t.Run("late_small_id_and_cross_month_correction", func(t *testing.T) {
		w, ctx, grant, task := scanFixture(t)
		aug, _, _ := af.DateBounds("2026-08-31")
		sep, _, _ := af.DateBounds(task.Date)
		scanRow(t, w, ctx, 100, aug+1, "old")
		if _, err := w.PassV2(ctx, grant); err != nil {
			t.Fatal(err)
		}
		scanRow(t, w, ctx, 2, sep+1, "late")
		if _, err := w.source.ExecContext(ctx, "UPDATE logs SET created_at=?,other='corrected' WHERE id=100", sep+2); err != nil {
			t.Fatal(err)
		}
		result, err := w.ScanDateV3(ctx, grant, task)
		if err != nil || result.State != "succeeded" || result.ScannedRows != 2 {
			t.Fatalf("backfill: %+v %v", result, err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM logs_202608") != 0 || writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM logs_202609") != 2 {
			t.Fatal("cross-month relocation left duplicate/missing rows")
		}
		if writerTestScalar(t, ctx, w.target, "SELECT after_id FROM archive_checkpoints WHERE stream_key='incremental'") != 100 {
			t.Fatal("scan reset incremental")
		}
	})
	t.Run("empty_day_transaction_and_retry_does_not_publish", func(t *testing.T) {
		w, ctx, grant, task := scanFixture(t)
		s, err := w.ScanDateV3(ctx, grant, task)
		if err != nil || s.State != "succeeded" || !s.EmptyCandidate || s.AfterID != 0 || s.BatchID == "" || s.Validate() != nil {
			t.Fatalf("empty: %+v %v", s, err)
		}
		var state string
		if err := w.target.QueryRowContext(ctx, "SELECT state FROM archive_days WHERE log_date=?", task.Date).Scan(&state); err != nil || state != "empty_candidate" {
			t.Fatalf("day: %q %v", state, err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_batch_receipts") != 1 {
			t.Fatal("empty page has no receipt")
		}
		s2, err := w.ScanDateV3(ctx, grant, task)
		if err != nil || s2.BatchID != s.BatchID {
			t.Fatalf("empty replay %+v %v", s2, err)
		}
		afterFailure, err := w.recordScanFailure(ctx, grant, task, context.DeadlineExceeded)
		if err != nil || afterFailure.State != "succeeded" || afterFailure.BatchID != s.BatchID {
			t.Fatalf("readback failure regressed committed terminal result: %+v %v", afterFailure, err)
		}
		if _, err := w.PassV2(ctx, grant); err != nil {
			t.Fatalf("first scan made empty incremental look legacy: %v", err)
		}
	})
	t.Run("unknown_and_cleared_history_are_not_empty_coverage", func(t *testing.T) {
		w, ctx, grant, task := scanFixture(t)
		task.Policy.SourceRetainedFrom = ""
		s, err := w.ScanDateV3(ctx, grant, task)
		if err != nil || s.State != "blocked" || s.ErrorCode != "source_history_unknown" || s.EmptyCandidate {
			t.Fatalf("unknown: %+v %v", s, err)
		}
		task.Attempt++
		task.Policy.SourceRetainedFrom = "2026-09-02"
		s, err = w.ScanDateV3(ctx, grant, task)
		if err == nil || s.State != "blocked" || s.ErrorCode != "source_cleared" || s.EmptyCandidate {
			t.Fatalf("cleared: %+v %v", s, err)
		}
	})
	t.Run("missing_and_wrong_order_index_are_persisted", func(t *testing.T) {
		w, ctx, grant, task := scanFixture(t)
		if _, err := w.source.ExecContext(ctx, "ALTER TABLE logs DROP INDEX idx_created,ADD INDEX bad(created_at,type,id)"); err != nil {
			t.Fatal(err)
		}
		s, err := w.ScanDateV3(ctx, grant, task)
		if err == nil || s.State != "blocked" || s.ErrorCode != "source_index_missing" || s.Validate() != nil {
			t.Fatalf("index: %+v %v", s, err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_batch_receipts") != 0 {
			t.Fatal("index failure produced a receipt")
		}
	})
	t.Run("oversized_row_blocks_and_manual_attempt_repairs", func(t *testing.T) {
		w, ctx, grant, task := scanFixture(t)
		from, _, _ := af.DateBounds(task.Date)
		scanRow(t, w, ctx, 9, from+1, strings.Repeat("x", 2000))
		task.Policy.Budget.MaxBytes = 1024
		task.Policy.Budget.MaxRowBytes = 1024
		s, err := w.ScanDateV3(ctx, grant, task)
		if err == nil || s.State != "blocked" || s.ErrorCode != "row_too_large" || s.AfterID != 0 {
			t.Fatalf("oversized: %+v %v", s, err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_ingest_issues WHERE resolved_at IS NULL") != 1 {
			t.Fatal("oversized issue not durable")
		}
		if writerTestScalar(t, ctx, w.target, "SELECT unscoped_blocking_issues FROM archive_dataset_meta") != 1 {
			t.Fatal("oversized scan row did not block global billing")
		}
		if _, err := w.source.ExecContext(ctx, "UPDATE logs SET other='fixed' WHERE id=9"); err != nil {
			t.Fatal(err)
		}
		task.Attempt++
		s, err = w.ScanDateV3(ctx, grant, task)
		if err != nil || s.State != "succeeded" || s.AfterID != 9 {
			t.Fatalf("repair: %+v %v", s, err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_ingest_issues WHERE resolved_at IS NULL") != 0 {
			t.Fatal("repair left stale row issue")
		}
		if writerTestScalar(t, ctx, w.target, "SELECT unscoped_blocking_issues FROM archive_dataset_meta") != 0 {
			t.Fatal("repair retained global oversized blocker")
		}
	})
	t.Run("frozen_empty_date_keeps_cursor_and_backoff", func(t *testing.T) {
		w, ctx, grant, task := scanFixture(t)
		if err := w.initializeScan(ctx, grant, task); err != nil {
			t.Fatal(err)
		}
		if _, err := w.target.ExecContext(ctx, "UPDATE archive_days SET freeze_task_id=UNHEX(REPEAT('c',32)) WHERE log_date=?", task.Date); err != nil {
			t.Fatal(err)
		}
		s, err := w.ScanDateV3(ctx, grant, task)
		if !errors.Is(err, ErrWriterFrozen) || s.State != "retry_wait" || s.RetryAfterUnix <= time.Now().Unix() || s.BatchID != "" {
			t.Fatalf("frozen %+v %v", s, err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_batch_receipts") != 0 {
			t.Fatal("frozen empty scan committed")
		}
	})
	t.Run("cursor_corruption_is_rejected", func(t *testing.T) {
		w, ctx, grant, task := scanFixture(t)
		from, _, _ := af.DateBounds(task.Date)
		scanRow(t, w, ctx, 2, from+1, "a")
		scanRow(t, w, ctx, 3, from+2, "b")
		w.SetBatchSize(1)
		if _, err := w.ScanDateV3(ctx, grant, task); err != nil {
			t.Fatal(err)
		}
		if _, err := w.target.ExecContext(ctx, "UPDATE archive_checkpoints SET after_created_unix=after_created_unix+1 WHERE stream_key=?", scanStream(task)); err != nil {
			t.Fatal(err)
		}
		if _, err := w.ScanDateV3(ctx, grant, task); err == nil {
			t.Fatal("corrupt scan cursor accepted")
		}
	})
	t.Run("current_day_is_not_scanned", func(t *testing.T) {
		w, ctx, grant, task := scanFixture(t)
		var now int64
		if err := w.source.QueryRowContext(ctx, "SELECT UNIX_TIMESTAMP()").Scan(&now); err != nil {
			t.Fatal(err)
		}
		task.Date = time.Unix(now, 0).In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02")
		s, err := w.ScanDateV3(ctx, grant, task)
		if err == nil || s.ErrorCode != "date_not_ready" || s.State != "retry_wait" {
			t.Fatalf("today %+v %v", s, err)
		}
	})
	t.Run("source_lock_wait_obeys_runtime_duration_budget", func(t *testing.T) {
		w, ctx, grant, task := scanFixture(t)
		w.source.SetMaxOpenConns(2)
		conn, err := w.source.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err = conn.ExecContext(ctx, "LOCK TABLES logs WRITE"); err != nil {
			t.Fatal(err)
		}
		defer conn.ExecContext(context.Background(), "UNLOCK TABLES")
		budget := af.DefaultScanBudget()
		budget.MaxDurationMillis = 1000
		w.SetScanBudget(budget)
		started := time.Now()
		s, err := w.ScanDateV3(ctx, grant, task)
		if err == nil || time.Since(started) > 3*time.Second || s.State != "retry_wait" || s.BatchID != "" {
			t.Fatalf("unbounded/false-complete slow source: %+v %v elapsed=%v", s, err, time.Since(started))
		}
		if writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_batch_receipts") != 0 {
			t.Fatal("timed-out read advanced scan")
		}
	})
}

func TestIncrementalBudgetMySQL(t *testing.T) {
	t.Run("bytes_page_and_delay_idle", func(t *testing.T) {
		w, ctx, g, _ := scanFixture(t)
		from, _, _ := af.DateBounds("2026-09-01")
		for i := int64(1); i <= 3; i++ {
			scanRow(t, w, ctx, i, from+i, strings.Repeat("x", 600))
		}
		b := af.DefaultScanBudget()
		b.MaxBytes = 1024
		b.MaxRowBytes = 1024
		w.SetScanBudget(b)
		r, err := w.PassV2(ctx, g)
		if err != nil || r.Rows != 1 || !r.More || r.Metrics.ReadBytes > 1024 {
			t.Fatalf("budget %+v %v", r, err)
		}
		if _, err := w.source.ExecContext(ctx, "DELETE FROM logs WHERE id>1"); err != nil {
			t.Fatal(err)
		}
		scanRow(t, w, ctx, 4, time.Now().Unix(), "delayed")
		r, err = w.PassV2(ctx, g)
		if err != nil || r.More || r.AfterID != 1 || r.Rows != 0 {
			t.Fatalf("delay was busy backlog %+v %v", r, err)
		}
	})
	t.Run("oversized_incremental_issue_and_null_timestamp", func(t *testing.T) {
		w, ctx, g, _ := scanFixture(t)
		from, _, _ := af.DateBounds("2026-09-01")
		scanRow(t, w, ctx, 1, from, strings.Repeat("x", 2000))
		b := af.DefaultScanBudget()
		b.MaxBytes = 1024
		b.MaxRowBytes = 1024
		w.SetScanBudget(b)
		r, err := w.PassV2(ctx, g)
		if err == nil || r.Metrics.ErrorCode != "row_too_large" {
			t.Fatalf("oversized %+v %v", r, err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT after_id FROM archive_checkpoints WHERE stream_key='incremental'") != 0 || writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM archive_ingest_issues WHERE resolved_at IS NULL") != 1 {
			t.Fatal("oversized row skipped or not recorded")
		}
		if _, err := w.source.ExecContext(ctx, "UPDATE logs SET other='',created_at=NULL WHERE id=1"); err != nil {
			t.Fatal(err)
		}
		if _, err := w.PassV2(ctx, g); err != nil {
			t.Fatal(err)
		}
		if writerTestScalar(t, ctx, w.target, "SELECT unscoped_blocking_issues FROM archive_dataset_meta") != 1 || writerTestScalar(t, ctx, w.target, "SELECT COUNT(*) FROM logs_undated") != 1 {
			t.Fatal("NULL date escaped quarantine")
		}
	})
}

// The fixture uses an actual transaction to inject an acknowledgement failure
// after commit; receipt replay must not double-apply date rows or counters.
func TestScanReceiptRecoveryMySQL(t *testing.T) {
	w, ctx, g, task := scanFixture(t)
	from, _, _ := af.DateBounds(task.Date)
	if err := w.initializeScan(ctx, g, task); err != nil {
		t.Fatal(err)
	}
	b := reviewWriterBatch()
	b.ID = strings.Repeat("d", 32)
	b.Scan = &task
	b.Rows[0][1] = strconv.FormatInt(from+1, 10)
	b.AfterCreated = from + 1
	b.Completed = true
	b.SourceNow = time.Now().Unix()
	r, err := w.commitWriterBatch(ctx, g, b, writerFaultHooks{AfterCommit: func() error { return sql.ErrConnDone }})
	if err != nil || !r.Replayed {
		t.Fatalf("lost response %+v %v", r, err)
	}
	s, err := w.ScanDateV3(ctx, g, task)
	if err != nil || s.State != "succeeded" || s.ScannedRows != 1 {
		t.Fatalf("recovery %+v %v", s, err)
	}
	if writerTestScalar(t, ctx, w.target, "SELECT log_rows FROM log_daily_stats") != 1 {
		t.Fatal("receipt recovery double counted")
	}
}
