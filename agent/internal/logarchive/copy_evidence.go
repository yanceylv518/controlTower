package logarchive

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	af "controltower/internal/archivecontract"
)

const copyReconcileMethod = "copied_source_paged"

// Captured only after raw target rows have been read back and compared with
// the source page. The same transaction publishes rows, evidence and cursor.
func writeCopyEvidence(ctx context.Context, tx *sql.Tx, b writerBatch) error {
	id := archiveID(b.Scan.TaskID)
	var run, raw []byte
	var completed bool
	err := tx.QueryRowContext(ctx, `SELECT run_id,scan_json,completed FROM archive_scan_evidence WHERE task_id=? AND attempt=? FOR UPDATE`, id, b.Scan.Attempt).Scan(&run, &raw, &completed)
	s := newReconcileScans().Scans[0]
	if errors.Is(err, sql.ErrNoRows) {
		if b.BeforeID != 0 || b.BeforeCreated != 0 {
			return ErrWriterCheckpoint
		}
		value, e := newArchiveID()
		if e != nil {
			return e
		}
		run = archiveID(value)
	} else if err != nil {
		return err
	} else if completed || json.Unmarshal(raw, &s) != nil || s.AfterID != b.BeforeID || s.AfterCreated != b.BeforeCreated {
		return ErrWriterCheckpoint
	}
	for _, row := range b.Rows {
		h, e := s.add(b.Columns, row)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO archive_reconcile_issues(run_id,source_id,source_created_unix,source_row_hash,issue_kind,updated_at) VALUES(?,?,?,?,'missing_target',UTC_TIMESTAMP(6))`, run, s.AfterID, s.AfterCreated, h[:]); e != nil {
			return e
		}
	}
	if s.AfterID != b.AfterID || s.AfterCreated != b.AfterCreated {
		return ErrWriterCheckpoint
	}
	raw, err = json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO archive_scan_evidence(task_id,attempt,run_id,scan_json,last_batch_id,completed,updated_at) VALUES(?,?,?,?,?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE scan_json=VALUES(scan_json),last_batch_id=VALUES(last_batch_id),completed=VALUES(completed),updated_at=VALUES(updated_at)`, id, b.Scan.Attempt, run, string(raw), archiveID(b.ID), b.Completed)
	return err
}

// Old in-flight scans lack trustworthy ingestion evidence. Re-scan that date
// once rather than manufacturing a source digest from already archived rows.
func (w *Worker) copyScanNeedsRestart(ctx context.Context, t af.BackfillTask) (bool, error) {
	var count int
	err := w.target.QueryRowContext(ctx, `SELECT COUNT(*) FROM archive_scan_tasks t LEFT JOIN archive_scan_evidence e ON e.task_id=t.task_id AND e.attempt=t.attempt WHERE t.task_id=? AND t.attempt=? AND e.task_id IS NULL AND (t.scanned_rows>0 OR t.state IN ('succeeded','blocked'))`, archiveID(t.TaskID), t.Attempt).Scan(&count)
	return count != 0, err
}

func (w *Worker) initializeCopyReconcile(ctx context.Context, g af.WriterGrant, t af.ReconcileTask, scan af.BackfillTask) (ReconcileEvidence, error) {
	tx, err := w.target.BeginTx(ctx, nil)
	if err != nil {
		return ReconcileEvidence{}, err
	}
	defer tx.Rollback()
	meta, err := readWriterMeta(ctx, tx, g.Identity, true)
	if err != nil {
		return ReconcileEvidence{}, err
	}
	if err = requireWriter(meta, g); err != nil {
		return ReconcileEvidence{}, err
	}
	saved, err := readReconcileRun(ctx, tx, "task_id=? AND attempt=?", archiveID(t.TaskID), t.Attempt)
	if err == nil {
		if saved.Method != copyReconcileMethod || saved.Date != t.Date || saved.Policy != t.Policy || saved.Assurance != t.Assurance {
			return saved, ErrWriterCheckpoint
		}
		return saved, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return saved, err
	}
	var run, raw, batch, receipt, fp []byte
	var completed bool
	var scanned uint64
	var sourceNow, afterID, afterCreated int64
	var state string
	err = tx.QueryRowContext(ctx, `SELECT e.run_id,e.scan_json,e.last_batch_id,e.completed,t.scanned_rows,t.source_now_unix,t.state,c.after_id,c.after_created_unix,c.last_batch_id FROM archive_scan_evidence e JOIN archive_scan_tasks t ON t.task_id=e.task_id AND t.attempt=e.attempt JOIN archive_checkpoints c ON c.stream_key=? WHERE e.task_id=? AND e.attempt=?`, scanStream(scan), archiveID(scan.TaskID), scan.Attempt).Scan(&run, &raw, &batch, &completed, &scanned, &sourceNow, &state, &afterID, &afterCreated, &receipt)
	if err != nil {
		return saved, err
	}
	scans := newReconcileScans()
	scans.Phase = 3
	if !completed || state != "succeeded" || hex.EncodeToString(batch) != hex.EncodeToString(receipt) || json.Unmarshal(raw, &scans.Scans[2]) != nil || !scans.valid() || scans.Scans[2].Summary.Rows != scanned || scans.Scans[2].AfterID != afterID || scans.Scans[2].AfterCreated != afterCreated {
		return saved, ErrWriterCheckpoint
	}
	if t.Date != scan.Date || t.Policy != scan.Policy || t.Policy.SourceRetainedFrom == "" || !t.Assurance.Covers(t.Date, sourceNow) || !t.Assurance.Covers(t.Date, time.Now().Unix()) {
		return saved, af.ErrConflict
	}
	var revision uint64
	var frozen bool
	if err = tx.QueryRowContext(ctx, `SELECT mutation_revision,freeze_task_id IS NOT NULL FROM archive_days WHERE log_date=? FOR UPDATE`, t.Date).Scan(&revision, &frozen); err != nil {
		return saved, err
	}
	if frozen {
		return saved, ErrWriterFrozen
	}
	if err = tx.QueryRowContext(ctx, `SELECT schema_fingerprint FROM archive_dataset_meta WHERE singleton_id=1`).Scan(&fp); err != nil {
		return saved, err
	}
	policy, _ := json.Marshal(t.Policy)
	assurance, _ := json.Marshal(t.Assurance)
	scanJSON, _ := json.Marshal(scans)
	_, err = tx.ExecContext(ctx, `INSERT INTO archive_reconcile_runs(run_id,task_id,attempt,log_date,method,state,phase,policy_json,assurance_json,schema_fingerprint,start_revision,writer_epoch,progress_version,scan_json,summary_json,source_now_unix,catalog_revision,started_at,updated_at) VALUES(?,?,?,?,?,'running','target_second',?,?,?,?,?,1,?,'{}',?,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, run, archiveID(t.TaskID), t.Attempt, t.Date, copyReconcileMethod, string(policy), string(assurance), fp, revision, g.WriterEpoch, string(scanJSON), sourceNow, meta.revision)
	if err != nil {
		return saved, err
	}
	saved, err = readReconcileRun(ctx, tx, "run_id=?", run)
	if err != nil {
		return saved, err
	}
	if err = writerGuardBeforeCommit(ctx, tx, g); err != nil {
		return saved, err
	}
	return saved, tx.Commit()
}

// Verify only the target against the source evidence captured while copying.
// No source log reads, and no claim that the source was observed twice.
func (w *Worker) reconcileCopiedDate(ctx context.Context, g af.WriterGrant, t af.ReconcileTask, scan af.BackfillTask) (af.ReconcileStatus, error) {
	if g.Validate() != nil || t.Validate() != nil || scan.Validate() != nil || !g.Identity.Equal(t.Identity) || !g.Identity.Equal(scan.Identity) {
		return af.ReconcileStatus{}, af.ErrConflict
	}
	budget := w.effectiveScanBudget(t.Policy.Budget)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(budget.MaxDurationMillis)*time.Millisecond)
	defer cancel()
	status, err := w.scanStatus(ctx, g, scan)
	if err != nil {
		return af.ReconcileStatus{}, err
	}
	if status.State != "succeeded" {
		return af.ReconcileStatus{}, ErrWriterCheckpoint
	}
	r, err := w.initializeCopyReconcile(ctx, g, t, scan)
	if err != nil {
		return af.ReconcileStatus{}, err
	}
	if r.State != "running" {
		return r.status(), nil
	}
	started := time.Now()
	hashes, n, err := w.readReconcilePage(ctx, &r, budget, started)
	if err != nil {
		return r.status(), err
	}
	return w.commitReconcilePage(ctx, g, t, r, hashes, n, uint64(time.Since(started).Milliseconds()))
}
