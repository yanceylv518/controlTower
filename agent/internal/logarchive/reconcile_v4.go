package logarchive

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	af "controltower/internal/archivecontract"
)

const reconcileMethod = "stable_window_paged"

var reconcilePhases = [...]string{"source_first", "target_first", "source_second", "target_second", "completed"}

// ReconcileEvidence is an immutable terminal run plus its explicit assurance
// conditions. matched is not a sealed version and never proves source retention.
type ReconcileEvidence struct {
	RunID             string                   `json:"run_id"`
	TaskID            string                   `json:"task_id"`
	Attempt           int                      `json:"attempt"`
	Date              string                   `json:"date"`
	State             string                   `json:"state"`
	Method            string                   `json:"method"`
	Phase             string                   `json:"phase"`
	StartRevision     uint64                   `json:"start_revision,string"`
	FinalRevision     uint64                   `json:"final_revision,string"`
	SchemaFingerprint string                   `json:"schema_fingerprint"`
	Policy            af.CoveragePolicy        `json:"policy"`
	Assurance         af.VerificationAssurance `json:"assurance"`
	SourceSummary     ReconcileSummary         `json:"source_summary"`
	TargetSummary     ReconcileSummary         `json:"target_summary"`
	IssueCount        uint64                   `json:"issue_count,string"`
	ErrorCode         string                   `json:"error_code,omitempty"`
	WriterEpoch       uint64                   `json:"writer_epoch,string"`
	CatalogRevision   uint64                   `json:"catalog_revision,string"`
	ReadBytes         uint64                   `json:"read_bytes,string"`
	ElapsedMillis     uint64                   `json:"elapsed_millis,string"`
	SourceNowUnix     int64                    `json:"source_now_unix,string"`
	scans             reconcileScans
	progress          uint64
}

func readReconcileRun(ctx context.Context, db foundationQuery, where string, args ...any) (ReconcileEvidence, error) {
	var r ReconcileEvidence
	var run, task, fp, policy, assurance, scans, summary []byte
	var final sql.Null[uint64]
	var code sql.NullString
	err := db.QueryRowContext(ctx, `SELECT run_id,task_id,attempt,DATE_FORMAT(log_date,'%Y-%m-%d'),method,state,phase,policy_json,assurance_json,schema_fingerprint,start_revision,final_revision,writer_epoch,progress_version,scan_json,summary_json,issue_count,error_code,source_now_unix,read_bytes,elapsed_millis,catalog_revision FROM archive_reconcile_runs WHERE `+where, args...).Scan(&run, &task, &r.Attempt, &r.Date, &r.Method, &r.State, &r.Phase, &policy, &assurance, &fp, &r.StartRevision, &final, &r.WriterEpoch, &r.progress, &scans, &summary, &r.IssueCount, &code, &r.SourceNowUnix, &r.ReadBytes, &r.ElapsedMillis, &r.CatalogRevision)
	if err != nil {
		return r, err
	}
	r.RunID, r.TaskID, r.SchemaFingerprint = hex.EncodeToString(run), hex.EncodeToString(task), hex.EncodeToString(fp)
	r.FinalRevision, r.ErrorCode = final.V, code.String
	if len(run) != 16 || len(task) != 16 || len(fp) != 32 || json.Unmarshal(policy, &r.Policy) != nil || r.Policy.Validate() != nil || json.Unmarshal(assurance, &r.Assurance) != nil || r.Assurance.Validate() != nil || json.Unmarshal(scans, &r.scans) != nil || !r.scans.valid() || (r.Method != reconcileMethod && r.Method != copyReconcileMethod) || r.Phase != reconcilePhases[r.scans.Phase] {
		return r, ErrWriterCheckpoint
	}
	r.SourceSummary, r.TargetSummary = r.scans.Scans[2].Summary, r.scans.Scans[3].Summary
	if r.scans.Phase < 2 {
		r.SourceSummary, r.TargetSummary = r.scans.Scans[0].Summary, r.scans.Scans[1].Summary
	}
	if r.status().Validate() != nil || (r.State != "running" && !final.Valid) {
		return r, ErrWriterCheckpoint
	}
	if r.State == "matched" && r.Method == reconcileMethod && (!sameReconcileSummary(r.scans.Scans[0].Summary, r.scans.Scans[2].Summary) || !sameReconcileSummary(r.scans.Scans[1].Summary, r.scans.Scans[3].Summary) || !sameReconcileSummary(r.SourceSummary, r.TargetSummary)) {
		return r, ErrWriterCheckpoint
	}
	return r, nil
}

func (w *Worker) ReadReconcileV4(ctx context.Context, identity af.Identity, runID string) (ReconcileEvidence, error) {
	if identity.Validate() != nil {
		return ReconcileEvidence{}, af.ErrConflict
	}
	id, err := af.IDBytes(runID)
	if err != nil {
		return ReconcileEvidence{}, err
	}
	tx, err := w.target.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return ReconcileEvidence{}, err
	}
	defer tx.Rollback()
	if _, err = readWriterMeta(ctx, tx, identity, false); err != nil {
		return ReconcileEvidence{}, err
	}
	r, err := readReconcileRun(ctx, tx, "run_id=?", id)
	if err != nil {
		return r, err
	}
	return r, tx.Commit()
}

func (w *Worker) initializeReconcileV4(ctx context.Context, grant af.WriterGrant, t af.ReconcileTask, fp string) (ReconcileEvidence, error) {
	tx, err := w.target.BeginTx(ctx, nil)
	if err != nil {
		return ReconcileEvidence{}, err
	}
	defer tx.Rollback()
	meta, err := readWriterMeta(ctx, tx, grant.Identity, true)
	if err != nil {
		return ReconcileEvidence{}, err
	}
	if err = requireWriter(meta, grant); err != nil {
		return ReconcileEvidence{}, err
	}
	taskID, _ := af.IDBytes(t.TaskID)
	var latest int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt),0) FROM archive_reconcile_runs WHERE task_id=?`, taskID).Scan(&latest); err != nil {
		return ReconcileEvidence{}, err
	}
	if latest > t.Attempt {
		return ReconcileEvidence{}, ErrWriterCheckpoint
	}
	r, err := readReconcileRun(ctx, tx, "task_id=? AND attempt=?", taskID, t.Attempt)
	if err == nil {
		if r.Method != reconcileMethod || r.Date != t.Date || r.Policy != t.Policy || r.Assurance != t.Assurance {
			return r, ErrWriterCheckpoint
		}
		return r, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return r, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO archive_days(log_date,state,updated_at) VALUES(?,'verifying',UTC_TIMESTAMP(6))`, t.Date); err != nil {
		return r, err
	}
	var revision uint64
	var frozen bool
	if err = tx.QueryRowContext(ctx, `SELECT mutation_revision,freeze_task_id IS NOT NULL OR freeze_epoch IS NOT NULL OR freeze_revision IS NOT NULL OR freeze_until IS NOT NULL FROM archive_days WHERE log_date=? FOR UPDATE`, t.Date).Scan(&revision, &frozen); err != nil {
		return r, err
	}
	if frozen {
		return r, ErrWriterFrozen
	}
	var runID [16]byte
	if _, err = rand.Read(runID[:]); err != nil {
		return r, err
	}
	policy, _ := json.Marshal(t.Policy)
	assurance, _ := json.Marshal(t.Assurance)
	scans, _ := json.Marshal(newReconcileScans())
	schemaHash, err := hex.DecodeString(fp)
	if err != nil || len(schemaHash) != 32 {
		return r, ErrFoundationSchema
	}
	if err = bumpScanCatalog(ctx, tx, &meta, t.Date, "verifying"); err != nil {
		return r, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO archive_reconcile_runs(run_id,task_id,attempt,log_date,method,state,phase,policy_json,assurance_json,schema_fingerprint,start_revision,writer_epoch,progress_version,scan_json,summary_json,catalog_revision,started_at,updated_at) VALUES(?,?,?,?,?,'running','source_first',?,?,?,?,?,1,?,'{}',?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, runID[:], taskID, t.Attempt, t.Date, reconcileMethod, string(policy), string(assurance), schemaHash, revision, grant.WriterEpoch, string(scans), meta.revision)
	if err != nil {
		return r, err
	}
	if err = writerGuardBeforeCommit(ctx, tx, grant); err != nil {
		return r, err
	}
	if err = tx.Commit(); err != nil {
		return r, err
	}
	return readReconcileRun(ctx, w.target, "task_id=? AND attempt=?", taskID, t.Attempt)
}

type reconcileRowHash struct {
	id, created int64
	hash        [32]byte
}

// ReconcileDateV4 advances one independent, bounded scan page. Four full scans
// (source,target,source,target) are compared. No cross-page source snapshot is
// claimed; the retained-history declaration is recorded as an explicit premise.
func (w *Worker) ReconcileDateV4(ctx context.Context, grant af.WriterGrant, t af.ReconcileTask) (af.ReconcileStatus, error) {
	if grant.Validate() != nil || t.Validate() != nil || !grant.Identity.Equal(t.Identity) {
		return af.ReconcileStatus{}, af.ErrConflict
	}
	started := time.Now()
	budget := w.effectiveScanBudget(t.Policy.Budget)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(budget.MaxDurationMillis)*time.Millisecond)
	defer cancel()
	info, err := w.inspectFoundationCached(ctx, grant.Identity)
	if err != nil {
		return af.ReconcileStatus{}, err
	}
	r, err := w.initializeReconcileV4(ctx, grant, t, info.SchemaFingerprint)
	if err != nil {
		return af.ReconcileStatus{}, err
	}
	if r.State != "running" {
		s := r.status()
		s.WriterEpoch = grant.WriterEpoch
		return s, nil
	}
	code := ""
	if t.Policy.SourceRetainedFrom == "" || t.Policy.CoverageFrom == "" || t.Date < t.Policy.CoverageFrom {
		code = "source_history_unknown"
	} else if t.Date < t.Policy.SourceRetainedFrom {
		code = "source_cleared"
	}
	if code == "" {
		err = w.requireScanIndex(ctx)
		if err != nil {
			code = scanFailureCode(err)
		}
	}
	var sourceNow int64
	if code == "" && w.source.QueryRowContext(ctx, "SELECT UNIX_TIMESTAMP()").Scan(&sourceNow) != nil {
		code = "source_clock_unavailable"
	}
	_, to, _ := af.DateBounds(t.Date)
	delay := w.delay
	if delay <= 0 {
		delay = 5 * time.Minute
	}
	if code == "" && to > sourceNow-int64(delay/time.Second) {
		code = "date_not_ready"
	}
	if code == "" && (!t.Assurance.Covers(t.Date, sourceNow) || t.Assurance.StableBeforeUnix > sourceNow || !t.Assurance.Covers(t.Date, time.Now().Unix())) {
		code = "verification_expired"
	}
	if code == "" {
		fp, e := w.foundationFingerprints(ctx)
		if e != nil {
			code = "source_unavailable"
		} else if hex.EncodeToString(fp.schema[:]) != r.SchemaFingerprint || hex.EncodeToString(fp.source[:]) != info.SourceFingerprint {
			code = "schema_changed"
		}
	}
	var hashes []reconcileRowHash
	var readBytes uint64
	if code == "" {
		hashes, readBytes, err = w.readReconcilePage(ctx, &r, budget, started)
		if err != nil {
			code = scanFailureCode(err)
		}
	}
	if code != "" {
		// Transient transport/budget failures retain the last committed page.
		// The caller retries under a fresh grant; no partial digest is persisted.
		switch code {
		case "source_unavailable", "source_query_failed", "source_clock_unavailable", "target_unavailable", "budget_exhausted":
			return r.status(), &scanError{code: code}
		}
		r.State = "blocked"
		r.ErrorCode = code
	}
	r.SourceNowUnix = sourceNow
	return w.commitReconcilePage(ctx, grant, t, r, hashes, readBytes, uint64(time.Since(started).Milliseconds()))
}

func (r ReconcileEvidence) status() af.ReconcileStatus {
	return af.ReconcileStatus{TaskID: r.TaskID, RunID: r.RunID, Date: r.Date, Attempt: r.Attempt, WriterEpoch: r.WriterEpoch, State: r.State, Phase: r.Phase, Method: r.Method, StartRevision: r.StartRevision, FinalRevision: r.FinalRevision, SourceRows: r.SourceSummary.Rows, TargetRows: r.TargetSummary.Rows, SourceDigest: r.SourceSummary.Digest, TargetDigest: r.TargetSummary.Digest, IssueCount: r.IssueCount, ErrorCode: r.ErrorCode, CatalogRevision: r.CatalogRevision, ProgressVersion: r.progress}
}

func (w *Worker) readReconcilePage(ctx context.Context, r *ReconcileEvidence, b af.ScanBudget, started time.Time) ([]reconcileRowHash, uint64, error) {
	phase := r.scans.Phase
	if phase >= 4 {
		return nil, 0, ErrWriterCheckpoint
	}
	db, table := w.source, "logs"
	if phase%2 == 1 {
		db = w.target
		table = "logs_" + strings.ReplaceAll(r.Date[:7], "-", "")
		exists, err := foundationTableExists(ctx, db, table)
		if err != nil {
			return nil, 0, err
		}
		if !exists {
			r.scans.Phase++
			return nil, 0, nil
		}
	}
	definition, err := schema(ctx, db, table)
	if err != nil {
		return nil, 0, err
	}
	fp := sha256.Sum256([]byte(definition))
	if hex.EncodeToString(fp[:]) != r.SchemaFingerprint {
		return nil, 0, ErrFoundationSchema
	}
	s := &r.scans.Scans[phase]
	from, to, _ := af.DateBounds(r.Date)
	rows, err := db.QueryContext(ctx, "SELECT * FROM "+quote(table)+` WHERE created_at>=? AND created_at<? AND (created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?`, from, to, s.AfterCreated, s.AfterCreated, s.AfterID, b.MaxRows+1)
	if err != nil {
		return nil, 0, &scanError{code: "source_query_failed"}
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, 0, err
	}
	var hashes []reconcileRowHash
	var readBytes uint64
	done := true
	for rows.Next() {
		if len(hashes) >= b.MaxRows || time.Since(started) > time.Duration(b.MaxDurationMillis)*time.Millisecond*3/4 {
			done = false
			break
		}
		raw := make([]sql.RawBytes, len(columns))
		dest := make([]any, len(raw))
		for i := range raw {
			dest[i] = &raw[i]
		}
		if rows.Scan(dest...) != nil {
			return nil, 0, &scanError{code: "source_query_failed"}
		}
		var size uint64
		row := make([]any, len(raw))
		for i, v := range raw {
			size += uint64(len(v))
			if v != nil {
				row[i] = string(v)
			}
		}
		if size > b.MaxRowBytes {
			return nil, 0, &scanError{code: "row_too_large"}
		}
		if readBytes+size > b.MaxBytes {
			done = false
			break
		}
		h, err := s.add(columns, row)
		if err != nil {
			return nil, 0, err
		}
		if s.AfterCreated < from || s.AfterCreated >= to {
			return nil, 0, ErrWriterCheckpoint
		}
		hashes = append(hashes, reconcileRowHash{id: s.AfterID, created: s.AfterCreated, hash: h})
		readBytes += size
	}
	if rows.Err() != nil {
		return nil, 0, &scanError{code: "source_query_failed"}
	}
	if !done && len(hashes) == 0 {
		return nil, 0, &scanError{code: "budget_exhausted"}
	}
	if done {
		r.scans.Phase++
	}
	return hashes, readBytes, nil
}
