package logarchive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	af "controltower/internal/archivecontract"
)

func repairFixture(t *testing.T) (*Worker, context.Context, af.WriterGrant, af.BackfillTask, int64) {
	t.Helper()
	w, ctx, grant, task := scanFixture(t)
	from, _, _ := af.DateBounds(task.Date)
	scanRow(t, w, ctx, 1, from+1, "source-original")
	if _, err := w.PassV2(ctx, grant); err != nil {
		t.Fatal(err)
	}
	return w, ctx, grant, task, from + 1
}

func repairTargetText(t *testing.T, ctx context.Context, w *Worker) string {
	t.Helper()
	var value string
	if err := w.target.QueryRowContext(ctx, `SELECT other FROM logs_202609 WHERE id=1`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func receiptCohort(t *testing.T, ctx context.Context, w *Worker, batchID string) []string {
	t.Helper()
	id, _ := af.IDBytes(batchID)
	var raw []byte
	if err := w.target.QueryRowContext(ctx, `SELECT cohort_dates_json FROM archive_batch_receipts WHERE batch_id=?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result []string
	if len(raw) == 0 || json.Unmarshal(raw, &result) != nil || result == nil {
		t.Fatal("new receipt has missing/null cohort", string(raw))
	}
	return result
}

func TestRawRepairV4MySQL(t *testing.T) {
	t.Run("missing_and_corrupt_identical_ledger_are_audited", func(t *testing.T) {
		for _, missing := range []bool{true, false} {
			t.Run(strconv.FormatBool(missing), func(t *testing.T) {
				w, ctx, g, task, _ := repairFixture(t)
				beforeRevision := writerTestScalar(t, ctx, w.target, `SELECT mutation_revision FROM archive_days WHERE log_date=?`, task.Date)
				query := `UPDATE logs_202609 SET other='target-corrupt' WHERE id=1`
				if missing {
					query = `DELETE FROM logs_202609 WHERE id=1`
				}
				if _, err := w.target.ExecContext(ctx, query); err != nil {
					t.Fatal(err)
				}
				s, err := w.ScanDateV3(ctx, g, task)
				if err != nil || s.State != "succeeded" || s.AfterID != 1 {
					t.Fatalf("repair %+v %v", s, err)
				}
				if got := repairTargetText(t, ctx, w); got != "source-original" {
					t.Fatal("raw not restored", got)
				}
				if writerTestScalar(t, ctx, w.target, `SELECT mutation_revision FROM archive_days WHERE log_date=?`, task.Date) != beforeRevision+1 {
					t.Fatal("identical ledger repair did not mutate day")
				}
				if writerTestScalar(t, ctx, w.target, `SELECT quota FROM log_daily_stats WHERE period_key=?`, task.Date) != 50 {
					t.Fatal("repair double counted quota")
				}
				if writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_raw_repairs WHERE source_id=1 AND after_row_hash=(SELECT raw_row_hash FROM archive_log_state WHERE id=1)`) != 1 {
					t.Fatal("missing immutable repair audit")
				}
				if writerTestScalar(t, ctx, w.target, `SELECT before_row_hash IS NULL FROM archive_raw_repairs`) != boolInt64(missing) {
					t.Fatal("missing vs changed before hash lost")
				}
				if len(receiptCohort(t, ctx, w, s.BatchID)) != 0 {
					t.Fatal("same-date repair became cross-day cohort")
				}
				if _, err = w.ScanDateV3(ctx, g, task); err != nil || writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_raw_repairs`) != 1 {
					t.Fatal("replay duplicated repair audit", err)
				}
			})
		}
	})
	t.Run("recent_replay_cannot_repair_target_damage", func(t *testing.T) {
		w, ctx, g, task, _ := repairFixture(t)
		task.Type = "recent_backfill"
		if _, err := w.target.ExecContext(ctx, `UPDATE logs_202609 SET other='damaged' WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		if _, err := w.ScanDateV3(ctx, g, task); err == nil {
			t.Fatal("recent replay silently repaired")
		}
		if repairTargetText(t, ctx, w) != "damaged" || writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_raw_repairs`) != 0 {
			t.Fatal("recent replay mutated damaged target")
		}
	})
	t.Run("actual_cross_day_is_frozen_and_dirtied_with_cohort", func(t *testing.T) {
		w, ctx, g, task, _ := repairFixture(t)
		actualDate := "2026-08-31"
		actual, _, _ := af.DateBounds(actualDate)
		if _, err := w.target.ExecContext(ctx, `UPDATE logs_202609 SET created_at=? WHERE id=1`, actual+1); err != nil {
			t.Fatal(err)
		}
		if _, err := w.target.ExecContext(ctx, `INSERT INTO archive_days(log_date,state,freeze_epoch,updated_at) VALUES(?,'verified',99,UTC_TIMESTAMP(6))`, actualDate); err != nil {
			t.Fatal(err)
		}
		if _, err := w.ScanDateV3(ctx, g, task); !errors.Is(err, ErrWriterFrozen) {
			t.Fatal("actual date freeze bypassed", err)
		}
		if writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_raw_repairs`) != 0 {
			t.Fatal("frozen repair audit committed")
		}
		if _, err := w.target.ExecContext(ctx, `UPDATE archive_days SET freeze_epoch=NULL WHERE log_date=?`, actualDate); err != nil {
			t.Fatal(err)
		}
		task.Attempt++
		s, err := w.ScanDateV3(ctx, g, task)
		if err != nil || s.State != "succeeded" {
			t.Fatalf("crossday repair %+v %v", s, err)
		}
		if !reflect.DeepEqual(receiptCohort(t, ctx, w, s.BatchID), []string{actualDate, task.Date}) {
			t.Fatal("actual/source crossday cohort missing")
		}
		var state string
		if err := w.target.QueryRowContext(ctx, `SELECT state FROM archive_days WHERE log_date=?`, actualDate).Scan(&state); err != nil || state != "dirty" {
			t.Fatal("actual previous day not dirtied", state, err)
		}
		if writerTestScalar(t, ctx, w.target, `SELECT mutation_revision FROM archive_days WHERE log_date=?`, actualDate) != 1 {
			t.Fatal("actual date revision not advanced")
		}
	})
	t.Run("target_extra_is_retained", func(t *testing.T) {
		w, ctx, g, task, created := repairFixture(t)
		if _, err := w.target.ExecContext(ctx, `INSERT INTO logs_202609(id,created_at,type,quota,other,prompt_tokens,completion_tokens) VALUES(999,?,2,777,'target-extra',1,1)`, created); err != nil {
			t.Fatal(err)
		}
		if _, err := w.target.ExecContext(ctx, `UPDATE logs_202609 SET other='damaged' WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		if _, err := w.ScanDateV3(ctx, g, task); err != nil {
			t.Fatal(err)
		}
		if writerTestScalar(t, ctx, w.target, `SELECT quota FROM logs_202609 WHERE id=999`) != 777 || writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_raw_repairs WHERE source_id=999`) != 0 {
			t.Fatal("target-only row was changed")
		}
	})
	t.Run("unscoped_damage_blocks_once_and_success_clears_atomically", func(t *testing.T) {
		w, ctx, g, task, created := repairFixture(t)
		if _, err := w.target.ExecContext(ctx, `UPDATE logs_202609 SET created_at=NULL WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		for attempt := 1; attempt <= 2; attempt++ {
			task.Attempt = attempt
			s, err := w.ScanDateV3(ctx, g, task)
			if err == nil || s.State != "blocked" || s.ErrorCode != "repair_unscoped_target" {
				t.Fatalf("unscoped %+v %v", s, err)
			}
			if writerTestScalar(t, ctx, w.target, `SELECT unscoped_blocking_issues FROM archive_dataset_meta`) != 1 {
				t.Fatal("global blocker missing/double counted")
			}
		}
		if writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_raw_repairs`) != 0 {
			t.Fatal("unknown damaged date was rewritten")
		}
		// The operator supplies a scoped timestamp; its different raw hash is
		// then repaired from the source by a new explicit attempt.
		if _, err := w.target.ExecContext(ctx, `UPDATE logs_202609 SET created_at=?,other='damaged-but-scoped' WHERE id=1`, created); err != nil {
			t.Fatal(err)
		}
		task.Attempt = 3
		if _, err := w.ScanDateV3(ctx, g, task); err != nil {
			t.Fatal(err)
		}
		if writerTestScalar(t, ctx, w.target, `SELECT unscoped_blocking_issues FROM archive_dataset_meta`) != 0 || writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_ingest_issues WHERE error_code='repair_unscoped_target' AND resolved_at IS NULL`) != 0 {
			t.Fatal("successful repair did not clear global blocker")
		}
	})
	t.Run("audit_raw_revision_and_cursor_roll_back_together", func(t *testing.T) {
		w, ctx, g, task, created := repairFixture(t)
		if _, err := w.target.ExecContext(ctx, `UPDATE logs_202609 SET other='damaged' WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		if err := w.initializeScan(ctx, g, task); err != nil {
			t.Fatal(err)
		}
		b := reviewWriterBatch()
		b.ID = strings.Repeat("e", 32)
		b.Scan = &task
		b.Rows[0][1] = strconv.FormatInt(created, 10)
		b.Rows[0][4] = "source-original"
		b.AfterCreated = created
		revision := writerTestScalar(t, ctx, w.target, `SELECT mutation_revision FROM archive_days WHERE log_date=?`, task.Date)
		injected := errors.New("repair injected rollback")
		if _, err := w.commitWriterBatch(ctx, g, b, writerFaultHooks{BeforeCommit: func(*sql.Tx) error { return injected }}); !errors.Is(err, injected) {
			t.Fatal(err)
		}
		if repairTargetText(t, ctx, w) != "damaged" || writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_raw_repairs`) != 0 || writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_checkpoints WHERE stream_key=?`, b.stream()) != 0 || writerTestScalar(t, ctx, w.target, `SELECT mutation_revision FROM archive_days WHERE log_date=?`, task.Date) != revision {
			t.Fatal("failed repair partially committed")
		}
		if _, err := w.commitWriterBatch(ctx, g, b, writerFaultHooks{AfterCommit: func() error { return sql.ErrConnDone }}); err != nil {
			t.Fatal("lost commit response did not recover", err)
		}
		if _, err := w.commitWriterBatch(ctx, g, b, writerFaultHooks{}); err != nil {
			t.Fatal(err)
		}
		if repairTargetText(t, ctx, w) != "source-original" || writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_raw_repairs`) != 1 {
			t.Fatal("repair replay duplicated changes")
		}
	})
	t.Run("ordinary_multiday_batch_has_no_cohort", func(t *testing.T) {
		w, ctx, g, _ := scanFixture(t)
		aug, _, _ := af.DateBounds("2026-08-31")
		sep, _, _ := af.DateBounds("2026-09-01")
		scanRow(t, w, ctx, 1, aug+1, "first")
		scanRow(t, w, ctx, 2, sep+1, "second")
		result, err := w.PassV2(ctx, g)
		if err != nil {
			t.Fatal(err)
		}
		if len(receiptCohort(t, ctx, w, result.BatchID)) != 0 {
			t.Fatal("independent multiday rows became cohort")
		}
	})
	t.Run("source_crossday_correction_has_cohort", func(t *testing.T) {
		w, ctx, g, task, _ := repairFixture(t)
		task.Date = "2026-09-02"
		created, _, _ := af.DateBounds(task.Date)
		if _, err := w.source.ExecContext(ctx, `UPDATE logs SET created_at=? WHERE id=1`, created+1); err != nil {
			t.Fatal(err)
		}
		s, err := w.ScanDateV3(ctx, g, task)
		if err != nil || !reflect.DeepEqual(receiptCohort(t, ctx, w, s.BatchID), []string{"2026-09-01", "2026-09-02"}) {
			t.Fatal("ordinary source correction lost cohort", err)
		}
	})
	t.Run("destination_duplicate_same_id_is_not_overwritten_or_deleted", func(t *testing.T) {
		w, ctx, g, task := scanFixture(t)
		aug, _, _ := af.DateBounds("2026-08-31")
		sep, _, _ := af.DateBounds(task.Date)
		scanRow(t, w, ctx, 1, aug+1, "old-source")
		if _, err := w.PassV2(ctx, g); err != nil {
			t.Fatal(err)
		}
		if err := ensureMonthlyTables(ctx, w.target, []string{"202609"}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.target.ExecContext(ctx, `INSERT INTO logs_202609(id,created_at,type,quota,other,prompt_tokens,completion_tokens) VALUES(1,?,2,777,'target-extra',1,1)`, sep+1); err != nil {
			t.Fatal(err)
		}
		if _, err := w.source.ExecContext(ctx, `UPDATE logs SET created_at=? WHERE id=1`, sep+1); err != nil {
			t.Fatal(err)
		}
		s, err := w.ScanDateV3(ctx, g, task)
		if !errors.Is(err, ErrWriterCheckpoint) || s.State != "blocked" {
			t.Fatalf("duplicate %+v %v", s, err)
		}
		if writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM logs_202608 WHERE id=1`) != 1 || writerTestScalar(t, ctx, w.target, `SELECT quota FROM logs_202609 WHERE id=1`) != 777 || writerTestScalar(t, ctx, w.target, `SELECT COUNT(*) FROM archive_raw_repairs`) != 0 {
			t.Fatal("same-id target-extra was destroyed")
		}
	})
}

func boolInt64(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
