package logarchive

import (
	"bytes"
	"context"
	"encoding/json"
	"math"

	af "controltower/internal/archivecontract"
)

func (w *Worker) commitReconcilePage(ctx context.Context, grant af.WriterGrant, t af.ReconcileTask, r ReconcileEvidence, hashes []reconcileRowHash, readBytes, elapsed uint64) (af.ReconcileStatus, error) {
	// Never return an in-memory terminal state when its transaction failed.
	// An ambiguous COMMIT is recovered by reading this same run on the next step.
	var committed af.ReconcileStatus
	tx, err := w.target.BeginTx(ctx, nil)
	if err != nil {
		return committed, err
	}
	defer tx.Rollback()
	meta, err := readWriterMeta(ctx, tx, grant.Identity, true)
	if err != nil {
		return committed, err
	}
	if err = requireWriter(meta, grant); err != nil {
		return committed, err
	}
	taskID, _ := af.IDBytes(t.TaskID)
	runID, _ := af.IDBytes(r.RunID)
	var latest int
	if err = tx.QueryRowContext(ctx, `SELECT MAX(attempt) FROM archive_reconcile_runs WHERE task_id=?`, taskID).Scan(&latest); err != nil {
		return committed, err
	}
	if latest != t.Attempt {
		return committed, ErrWriterCheckpoint
	}
	saved, err := readReconcileRun(ctx, tx, "run_id=? FOR UPDATE", runID)
	if err != nil {
		return committed, err
	}
	committed = saved.status()
	committed.WriterEpoch = grant.WriterEpoch
	if saved.progress != r.progress || saved.State != "running" {
		return committed, tx.Commit()
	}
	var revision uint64
	var frozen bool
	var targetNow int64
	if err = tx.QueryRowContext(ctx, `SELECT mutation_revision,freeze_task_id IS NOT NULL OR freeze_epoch IS NOT NULL OR freeze_revision IS NOT NULL OR freeze_until IS NOT NULL,FLOOR(UNIX_TIMESTAMP()) FROM archive_days WHERE log_date=? FOR UPDATE`, t.Date).Scan(&revision, &frozen, &targetNow); err != nil {
		return committed, err
	}
	if frozen {
		return committed, ErrWriterFrozen
	}
	if revision != r.StartRevision {
		r.State = "blocked"
		r.ErrorCode = "target_drift"
	}
	if r.State == "running" && r.Assurance.ValidUntilUnix <= targetNow {
		r.State = "blocked"
		r.ErrorCode = "verification_expired"
	}
	if r.State == "running" && saved.scans.Phase >= 2 {
		for _, h := range hashes {
			if saved.scans.Phase == 2 {
				_, err = tx.ExecContext(ctx, `INSERT INTO archive_reconcile_issues(run_id,source_id,source_created_unix,source_row_hash,issue_kind,updated_at) VALUES(?,?,?,?,'missing_target',UTC_TIMESTAMP(6))`, runID, h.id, h.created, h.hash[:])
			} else {
				_, err = tx.ExecContext(ctx, `INSERT INTO archive_reconcile_issues(run_id,source_id,target_created_unix,target_row_hash,issue_kind,updated_at) VALUES(?,?,?,?,'extra_target',UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE target_created_unix=VALUES(target_created_unix),target_row_hash=VALUES(target_row_hash),issue_kind=IF(source_row_hash=VALUES(target_row_hash),'equal','different'),updated_at=UTC_TIMESTAMP(6)`, runID, h.id, h.created, h.hash[:])
			}
			if err != nil {
				return committed, err
			}
		}
	}
	r.Phase = reconcilePhases[r.scans.Phase]
	r.WriterEpoch = grant.WriterEpoch
	r.ReadBytes += readBytes
	r.ElapsedMillis += elapsed
	if r.progress == math.MaxUint64 {
		return committed, ErrWriterCheckpoint
	}
	r.progress++
	dayState := ""
	if r.scans.Phase == 4 && r.State == "running" {
		s := r.scans.Scans
		switch {
		case r.Method == reconcileMethod && !sameReconcileSummary(s[0].Summary, s[2].Summary):
			r.State = "blocked"
			r.ErrorCode = "source_drift"
		case r.Method == reconcileMethod && !sameReconcileSummary(s[1].Summary, s[3].Summary):
			r.State = "blocked"
			r.ErrorCode = "target_drift"
		case meta.unscoped > 0:
			r.State = "blocked"
			r.ErrorCode = "global_blocked"
		case s[2].Summary.UnknownChargedRows > 0 || s[3].Summary.UnknownChargedRows > 0:
			r.State = "blocked"
			r.ErrorCode = "unknown_charged_type"
		case sameReconcileSummary(s[2].Summary, s[3].Summary):
			r.State = "matched"
		default:
			r.State = "mismatched"
		}
	}
	if r.State != "running" {
		r.FinalRevision = revision
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM archive_reconcile_issues WHERE run_id=? AND issue_kind<>'equal'`, runID).Scan(&r.IssueCount); err != nil {
			return committed, err
		}
		if r.State == "matched" && r.IssueCount != 0 {
			r.State = "blocked"
			r.ErrorCode = "checkpoint_conflict"
		}
		switch r.State {
		case "matched":
			dayState = "pending_verify"
		case "mismatched":
			dayState = "needs_fill"
		default:
			dayState = "blocked"
		}
		if err = bumpScanCatalog(ctx, tx, &meta, t.Date, dayState); err != nil {
			return committed, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE archive_days SET last_reconcile_run_id=? WHERE log_date=?`, runID, t.Date); err != nil {
			return committed, err
		}
	}
	r.CatalogRevision = meta.revision
	r.SourceSummary, r.TargetSummary = r.scans.Scans[2].Summary, r.scans.Scans[3].Summary
	if r.scans.Phase < 2 {
		r.SourceSummary, r.TargetSummary = r.scans.Scans[0].Summary, r.scans.Scans[1].Summary
	}
	scans, _ := json.Marshal(r.scans)
	summary, _ := json.Marshal(map[string]any{"source_first": r.scans.Scans[0].Summary, "target_first": r.scans.Scans[1].Summary, "source_second": r.scans.Scans[2].Summary, "target_second": r.scans.Scans[3].Summary})
	_, err = tx.ExecContext(ctx, `UPDATE archive_reconcile_runs SET state=?,phase=?,final_revision=IF(?, ?,NULL),writer_epoch=?,progress_version=?,scan_json=?,summary_json=?,issue_count=?,error_code=NULLIF(?,''),source_now_unix=?,read_bytes=?,elapsed_millis=?,catalog_revision=?,completed_at=IF(?,UTC_TIMESTAMP(6),NULL),updated_at=UTC_TIMESTAMP(6) WHERE run_id=?`, r.State, r.Phase, r.State != "running", r.FinalRevision, r.WriterEpoch, r.progress, string(scans), string(summary), r.IssueCount, r.ErrorCode, r.SourceNowUnix, r.ReadBytes, r.ElapsedMillis, r.CatalogRevision, r.State != "running", runID)
	if err != nil {
		return committed, err
	}
	persisted, err := readReconcileRun(ctx, tx, "run_id=?", runID)
	if err != nil {
		return committed, err
	}
	persistedScans, _ := json.Marshal(persisted.scans)
	if persisted.status() != r.status() || !bytes.Equal(persistedScans, scans) {
		return committed, ErrWriterCheckpoint
	}
	if err = writerGuardBeforeCommit(ctx, tx, grant); err != nil {
		return committed, err
	}
	if err = tx.Commit(); err != nil {
		return committed, err
	}
	return r.status(), nil
}
