package logarchive

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strings"
	"time"

	af "controltower/internal/archivecontract"
	facts "controltower/internal/archivefacts"
)

type sealDay struct {
	Date              string
	VersionID         string
	VersionNo         uint64
	PreviousVersionID string
	Revision          uint64
	Run               ReconcileEvidence
	Raw               reconcileScan
	Audit             reconcileScan
	FactState         []byte
	FactRows          uint64
	FactQuota         string
	ManifestHash      string
}
type sealProgress struct {
	Days  []sealDay
	Index int
	Audit bool
}
type sealBuild struct {
	ID                       string
	Task                     af.SealTask
	State                    string
	Epoch, Progress, Catalog uint64
	Error                    string
	Data                     sealProgress
}

func sealCode(code string) error { return &scanError{code: code} }
func newArchiveID() (string, error) {
	var id [16]byte
	_, err := rand.Read(id[:])
	return hex.EncodeToString(id[:]), err
}
func archiveID(id string) []byte  { b, _ := af.IDBytes(id); return b }
func archiveHash(h string) []byte { b, _ := hex.DecodeString(h); return b }
func (b sealBuild) status(epoch uint64) af.SealStatus {
	s := af.SealStatus{TaskID: b.Task.TaskID, BuildID: b.ID, Attempt: b.Task.Attempt, WriterEpoch: epoch, ProgressVersion: b.Progress, CatalogRevision: b.Catalog, State: b.State, ErrorCode: b.Error}
	if b.State == "succeeded" {
		for _, d := range b.Data.Days {
			s.Versions = append(s.Versions, af.SealedDay{Date: d.Date, VersionID: d.VersionID, VersionNo: d.VersionNo, ManifestHash: d.ManifestHash})
		}
	}
	return s
}
func readSealBuild(ctx context.Context, db foundationQuery, task af.SealTask) (sealBuild, error) {
	var b sealBuild
	var id, t, p []byte
	var code sql.NullString
	err := db.QueryRowContext(ctx, `SELECT build_id,task_json,state,writer_epoch,progress_version,progress_json,error_code,catalog_revision FROM archive_seal_builds WHERE task_id=? AND attempt=?`, archiveID(task.TaskID), task.Attempt).Scan(&id, &t, &b.State, &b.Epoch, &b.Progress, &p, &code, &b.Catalog)
	if err != nil {
		return b, err
	}
	b.ID, b.Error = hex.EncodeToString(id), code.String
	if len(id) != 16 || json.Unmarshal(t, &b.Task) != nil || json.Unmarshal(p, &b.Data) != nil {
		return b, ErrWriterCheckpoint
	}
	want, _ := json.Marshal(task)
	got, _ := json.Marshal(b.Task)
	if !bytes.Equal(want, got) || len(b.Data.Days) != len(task.Dates) || b.Data.Index < 0 || b.Data.Index > len(task.Dates) {
		return b, ErrWriterCheckpoint
	}
	for i, d := range b.Data.Days {
		if d.Date != task.Dates[i] || d.Run.Date != d.Date || d.Run.State != "matched" || d.Run.StartRevision != d.Revision || d.Run.FinalRevision != d.Revision {
			return b, ErrWriterCheckpoint
		}
	}
	return b, nil
}

// SealDaysV4 advances one bounded target-only build/audit page. Published
// versions are immutable; only the final short transaction updates day pointers.
func (w *Worker) SealDaysV4(ctx context.Context, g af.WriterGrant, t af.SealTask) (af.SealStatus, error) {
	if g.Validate() != nil || t.Validate() != nil || !g.Identity.Equal(t.Identity) {
		return af.SealStatus{}, af.ErrConflict
	}
	budget := w.effectiveScanBudget(t.Policy.Budget)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(budget.MaxDurationMillis)*time.Millisecond)
	defer cancel()
	if _, err := w.inspectFoundationCached(ctx, g.Identity); err != nil {
		return af.SealStatus{}, err
	}
	if err := w.ensureSealMonths(ctx, g, t.Dates); err != nil {
		return af.SealStatus{}, err
	}
	b, err := w.startSealBuild(ctx, g, t)
	if err != nil {
		state, code := "running", ""
		var se *scanError
		if errors.As(err, &se) && af.ValidVerifyError(se.code) {
			state, code = "blocked", se.code
		}
		return af.SealStatus{TaskID: t.TaskID, Attempt: t.Attempt, WriterEpoch: g.WriterEpoch, State: state, ErrorCode: code}, err
	}
	if b.State != "running" {
		return b.status(g.WriterEpoch), nil
	}
	result, err := w.advanceSealBuild(ctx, g, b, budget)
	if err == nil {
		return result, nil
	}
	// Only deterministic failures abandon an unpublished version. Transport/
	// deadline failures keep the frozen build resumable from its atomic cursor.
	var se *scanError
	if errors.As(err, &se) && af.ValidVerifyError(se.code) {
		recovery, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if status, e := w.abortSealBuild(recovery, g, t, se.code); e == nil {
			return status, err
		}
	}
	return b.status(g.WriterEpoch), err
}

func (w *Worker) startSealBuild(ctx context.Context, g af.WriterGrant, t af.SealTask) (sealBuild, error) {
	tx, err := w.target.BeginTx(ctx, nil)
	if err != nil {
		return sealBuild{}, err
	}
	defer tx.Rollback()
	meta, err := readWriterMeta(ctx, tx, g.Identity, true)
	if err != nil {
		return sealBuild{}, err
	}
	if err = requireWriter(meta, g); err != nil {
		return sealBuild{}, err
	}
	var latest int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt),0) FROM archive_seal_builds WHERE task_id=?`, archiveID(t.TaskID)).Scan(&latest); err != nil {
		return sealBuild{}, err
	}
	if latest > t.Attempt {
		return sealBuild{}, ErrWriterCheckpoint
	}
	b, err := readSealBuild(ctx, tx, t)
	if err == nil {
		return b, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return b, err
	}
	if meta.unscoped != 0 {
		return b, sealCode("global_blocked")
	}
	if err = checkSealCohort(ctx, tx, t.Dates); err != nil {
		return b, err
	}
	b.ID, err = newArchiveID()
	if err != nil {
		return b, err
	}
	b.Task, b.State, b.Epoch = t, "running", g.WriterEpoch
	b.Progress = 1
	for _, date := range t.Dates {
		var d sealDay
		d.Date = date
		var previous []byte
		var frozen bool
		var no uint64
		var blocking uint64
		err = tx.QueryRowContext(ctx, `SELECT mutation_revision,latest_version_no,current_version_id,freeze_task_id IS NOT NULL OR freeze_epoch IS NOT NULL OR freeze_revision IS NOT NULL OR freeze_until IS NOT NULL,blocking_issue_count FROM archive_days WHERE log_date=? FOR UPDATE`, date).Scan(&d.Revision, &no, &previous, &frozen, &blocking)
		if errors.Is(err, sql.ErrNoRows) {
			return b, sealCode("verification_required")
		}
		if err != nil {
			return b, err
		}
		if frozen {
			return b, sealCode("date_frozen")
		}
		if blocking != 0 {
			return b, sealCode("global_blocked")
		}
		if no >= math.MaxUint32 {
			return b, sealCode("build_conflict")
		}
		d.Run, err = readReconcileRun(ctx, tx, `log_date=? AND state='matched' AND start_revision=? AND final_revision=? ORDER BY completed_at DESC,run_id DESC LIMIT 1`, date, d.Revision, d.Revision)
		if errors.Is(err, sql.ErrNoRows) {
			return b, sealCode("verification_required")
		}
		if err != nil {
			return b, err
		}
		var now int64
		if err = tx.QueryRowContext(ctx, "SELECT UNIX_TIMESTAMP()").Scan(&now); err != nil {
			return b, err
		}
		if !d.Run.Assurance.Covers(date, now) || !d.Run.Assurance.Covers(date, time.Now().Unix()) {
			return b, sealCode("verification_expired")
		}
		if d.Run.IssueCount != 0 || !sameReconcileSummary(d.Run.SourceSummary, d.Run.TargetSummary) {
			return b, sealCode("verification_mismatch")
		}
		d.VersionID, err = newArchiveID()
		if err != nil {
			return b, err
		}
		d.VersionNo = no + 1
		d.PreviousVersionID = hex.EncodeToString(previous)
		d.Raw = newReconcileScans().Scans[0]
		d.Audit = newReconcileScans().Scans[0]
		d.FactQuota = "0"
		h := sha256.New()
		writerHashField(h, []byte("archive-day-facts-v1"))
		d.FactState, _ = h.(encoding.BinaryMarshaler).MarshalBinary()
		var prev any
		if len(previous) > 0 {
			prev = previous
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO archive_day_versions(day_version_id,log_date,version_no,previous_version_id,build_task_id,state,verified_mutation_revision,reconcile_run_id,parser_version,fact_schema_version,evidence_codec_version,storage_month,created_at) VALUES(?,?,?,?,?,'building',?,?,1,?,?,?,UTC_TIMESTAMP(6))`, archiveID(d.VersionID), date, d.VersionNo, prev, archiveID(b.ID), d.Revision, archiveID(d.Run.RunID), facts.FactSchemaVersion, facts.EvidenceCodecVersion, strings.ReplaceAll(date[:7], "-", ""))
		if err != nil {
			return b, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE archive_days SET freeze_task_id=?,freeze_epoch=?,freeze_revision=?,freeze_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 120 SECOND),latest_version_no=?,updated_at=UTC_TIMESTAMP(6) WHERE log_date=?`, archiveID(b.ID), g.WriterEpoch, d.Revision, d.VersionNo, date)
		if err != nil {
			return b, err
		}
		b.Data.Days = append(b.Data.Days, d)
	}
	raw, _ := json.Marshal(b.Data)
	task, _ := json.Marshal(t)
	_, err = tx.ExecContext(ctx, `INSERT INTO archive_seal_builds(build_id,task_id,attempt,task_json,state,writer_epoch,progress_version,progress_json,created_at,updated_at) VALUES(?,?,?,?,'running',?,1,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, archiveID(b.ID), archiveID(t.TaskID), t.Attempt, string(task), g.WriterEpoch, string(raw))
	if err != nil {
		return b, err
	}
	if err = writerGuardBeforeCommit(ctx, tx, g); err != nil {
		return b, err
	}
	return b, tx.Commit()
}

// Old receipts lack cohort metadata and are conservatively treated as a set.
// New receipts link only actual cross-date corrections. A correction cannot
// become partially current even when its dates straddle storage months.
func checkSealCohort(ctx context.Context, tx *sql.Tx, dates []string) error {
	selected := map[string]bool{}
	var min uint64 = math.MaxUint64
	for _, d := range dates {
		selected[d] = true
		var revision uint64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(v.publish_revision),0) FROM archive_days d LEFT JOIN archive_day_versions v ON v.day_version_id=d.current_version_id WHERE d.log_date=?`, d).Scan(&revision); err != nil {
			return err
		}
		if revision < min {
			min = revision
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT COALESCE(cohort_dates_json,affected_dates_json),CAST(JSON_UNQUOTE(JSON_EXTRACT(cursor_after_json,'$.catalog_revision')) AS UNSIGNED) FROM archive_batch_receipts WHERE JSON_LENGTH(COALESCE(cohort_dates_json,affected_dates_json))>1 AND CAST(JSON_UNQUOTE(JSON_EXTRACT(cursor_after_json,'$.catalog_revision')) AS UNSIGNED)>?`, min)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var rev uint64
		if rows.Scan(&raw, &rev) != nil {
			return ErrWriterCheckpoint
		}
		var cohort []string
		if json.Unmarshal(raw, &cohort) != nil {
			return ErrWriterCheckpoint
		}
		intersects := false
		for _, d := range cohort {
			if selected[d] {
				intersects = true
			}
		}
		if intersects {
			for _, d := range cohort {
				if !selected[d] {
					return sealCode("cohort_incomplete")
				}
			}
		}
	}
	return rows.Err()
}

func requireSealFreeze(ctx context.Context, tx *sql.Tx, g af.WriterGrant, b sealBuild) error {
	for _, d := range b.Data.Days {
		var rev, epoch, freezeRevision uint64
		var build []byte
		var blocked uint64
		if err := tx.QueryRowContext(ctx, `SELECT mutation_revision,freeze_task_id,freeze_epoch,freeze_revision,blocking_issue_count FROM archive_days WHERE log_date=? FOR UPDATE`, d.Date).Scan(&rev, &build, &epoch, &freezeRevision, &blocked); err != nil {
			return sealCode("build_conflict")
		}
		if rev != d.Revision || freezeRevision != rev || hex.EncodeToString(build) != b.ID || epoch > g.WriterEpoch || blocked != 0 {
			return sealCode("build_conflict")
		}
		var now int64
		if err := tx.QueryRowContext(ctx, "SELECT UNIX_TIMESTAMP()").Scan(&now); err != nil {
			return err
		}
		if !d.Run.Assurance.Covers(d.Date, now) || !d.Run.Assurance.Covers(d.Date, time.Now().Unix()) {
			return sealCode("verification_expired")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE archive_days SET freeze_epoch=?,freeze_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 120 SECOND) WHERE log_date=?`, g.WriterEpoch, d.Date); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) advanceSealBuild(ctx context.Context, g af.WriterGrant, prior sealBuild, budget af.ScanBudget) (af.SealStatus, error) {
	tx, err := w.target.BeginTx(ctx, nil)
	if err != nil {
		return af.SealStatus{}, err
	}
	defer tx.Rollback()
	meta, err := readWriterMeta(ctx, tx, g.Identity, true)
	if err != nil {
		return af.SealStatus{}, err
	}
	if err = requireWriter(meta, g); err != nil {
		return af.SealStatus{}, err
	}
	b, err := readSealBuild(ctx, tx, prior.Task)
	if err != nil {
		return af.SealStatus{}, err
	}
	if b.State != "running" {
		return b.status(g.WriterEpoch), nil
	}
	if b.Progress != prior.Progress {
		return af.SealStatus{}, ErrWriterCheckpoint
	}
	if meta.unscoped != 0 {
		return af.SealStatus{}, sealCode("global_blocked")
	}
	if err = requireSealFreeze(ctx, tx, g, b); err != nil {
		return af.SealStatus{}, err
	}
	if b.Data.Index < len(b.Data.Days) {
		d := &b.Data.Days[b.Data.Index]
		done, err := sealDayPage(ctx, tx, d, budget, b.Data.Audit)
		if err != nil {
			return af.SealStatus{}, err
		}
		if done {
			if !b.Data.Audit {
				if !sameReconcileSummary(d.Raw.Summary, d.Run.TargetSummary) || d.FactRows != d.Run.SourceSummary.ConsumeRows || d.FactQuota != d.Run.SourceSummary.ConsumeQuota {
					return af.SealStatus{}, sealCode("verification_mismatch")
				}
			} else {
				if !sameReconcileSummary(d.Audit.Summary, d.Raw.Summary) {
					return af.SealStatus{}, sealCode("target_drift")
				}
				var count uint64
				month := strings.ReplaceAll(d.Date[:7], "-", "")
				if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quote("billing_facts_"+month)+" WHERE day_version_id=?", archiveID(d.VersionID)).Scan(&count); err != nil {
					return af.SealStatus{}, err
				}
				if count != d.FactRows {
					return af.SealStatus{}, sealCode("fact_invalid")
				}
			}
			b.Data.Index++
		}
		if b.Data.Index == len(b.Data.Days) && !b.Data.Audit {
			b.Data.Index = 0
			b.Data.Audit = true
		}
	} else {
		if err = publishSealBuild(ctx, tx, g, &meta, &b); err != nil {
			return af.SealStatus{}, err
		}
	}
	b.Epoch = g.WriterEpoch
	b.Progress++
	raw, _ := json.Marshal(b.Data)
	_, err = tx.ExecContext(ctx, `UPDATE archive_seal_builds SET state=?,writer_epoch=?,progress_version=?,progress_json=?,catalog_revision=?,updated_at=UTC_TIMESTAMP(6),published_at=IF(?='succeeded',UTC_TIMESTAMP(6),NULL) WHERE build_id=?`, b.State, b.Epoch, b.Progress, string(raw), b.Catalog, b.State, archiveID(b.ID))
	if err != nil {
		return af.SealStatus{}, err
	}
	if err = writerGuardBeforeCommit(ctx, tx, g); err != nil {
		return af.SealStatus{}, err
	}
	if err = tx.Commit(); err != nil {
		return af.SealStatus{}, err
	}
	return b.status(g.WriterEpoch), nil
}

func publishSealBuild(ctx context.Context, tx *sql.Tx, g af.WriterGrant, meta *writerMeta, b *sealBuild) error {
	// Cohorts were checked before all selected dates were frozen under the
	// dataset lock. Every raw writer checks both sides of a cross-date move,
	// so no new intersecting cohort can appear before publication.
	if meta.revision == math.MaxUint64 {
		return sealCode("build_conflict")
	}
	meta.revision++
	for i := range b.Data.Days {
		d := &b.Data.Days[i]
		h := sha256.New()
		if h.(encoding.BinaryUnmarshaler).UnmarshalBinary(d.FactState) != nil {
			return ErrWriterCheckpoint
		}
		// A Go struct, not MySQL JSON serialization, defines the canonical manifest.
		manifest := struct {
			Version           int                      `json:"version"`
			Identity          af.Identity              `json:"identity"`
			Date              string                   `json:"date"`
			VersionID         string                   `json:"version_id"`
			VersionNo         uint64                   `json:"version_no,string"`
			PreviousVersionID string                   `json:"previous_version_id,omitempty"`
			MutationRevision  uint64                   `json:"mutation_revision,string"`
			RunID             string                   `json:"run_id"`
			Method            string                   `json:"method"`
			Assurance         af.VerificationAssurance `json:"assurance"`
			SchemaHash        string                   `json:"schema_hash"`
			Parser            string                   `json:"parser"`
			FactSchema, Codec int
			Raw               ReconcileSummary `json:"raw"`
			FactRows          uint64           `json:"fact_rows,string"`
			FactQuota         string           `json:"fact_quota"`
			FactDigest        string           `json:"fact_digest"`
			Cohort            []string         `json:"cohort"`
			PublishRevision   uint64           `json:"publish_revision,string"`
		}{1, g.Identity, d.Date, d.VersionID, d.VersionNo, d.PreviousVersionID, d.Revision, d.Run.RunID, d.Run.Method, d.Run.Assurance, d.Run.SchemaFingerprint, facts.ParserVersion, facts.FactSchemaVersion, facts.EvidenceCodecVersion, d.Raw.Summary, d.FactRows, d.FactQuota, hex.EncodeToString(h.Sum(nil)), b.Task.Dates, meta.revision}
		raw, err := json.Marshal(manifest)
		if err != nil {
			return err
		}
		digest, err := CanonicalManifestHash(raw)
		if err != nil {
			return err
		}
		d.ManifestHash = hex.EncodeToString(digest[:])
		changed, err := tx.ExecContext(ctx, `UPDATE archive_day_versions SET state='published',all_rows=?,consume_rows=?,consume_quota=?,raw_digest=?,facts_digest=?,manifest_json=?,manifest_hash=?,publish_revision=?,published_at=UTC_TIMESTAMP(6) WHERE day_version_id=? AND state='building'`, d.Raw.Summary.Rows, d.FactRows, d.FactQuota, archiveHash(d.Raw.Summary.Digest), h.Sum(nil), string(raw), digest[:], meta.revision, archiveID(d.VersionID))
		if err != nil {
			return err
		}
		n, err := changed.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return sealCode("build_conflict")
		}
		_, err = tx.ExecContext(ctx, `UPDATE archive_days SET current_version_id=?,state='sealed',last_reconcile_run_id=?,catalog_revision=?,freeze_task_id=NULL,freeze_epoch=NULL,freeze_revision=NULL,freeze_until=NULL,updated_at=UTC_TIMESTAMP(6) WHERE log_date=?`, archiveID(d.VersionID), archiveID(d.Run.RunID), meta.revision, d.Date)
		if err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE archive_dataset_meta SET catalog_revision=?,updated_at=UTC_TIMESTAMP(6) WHERE singleton_id=1`, meta.revision)
	b.Catalog, b.State = meta.revision, "succeeded"
	return err
}

func (w *Worker) abortSealBuild(ctx context.Context, g af.WriterGrant, t af.SealTask, code string) (af.SealStatus, error) {
	tx, err := w.target.BeginTx(ctx, nil)
	if err != nil {
		return af.SealStatus{}, err
	}
	defer tx.Rollback()
	meta, err := readWriterMeta(ctx, tx, g.Identity, true)
	if err != nil {
		return af.SealStatus{}, err
	}
	if err = requireWriter(meta, g); err != nil {
		return af.SealStatus{}, err
	}
	b, err := readSealBuild(ctx, tx, t)
	if err != nil {
		return af.SealStatus{}, err
	}
	if b.State != "running" {
		return b.status(g.WriterEpoch), nil
	}
	// Retrying keeps prior immutable staging rows for audit; a new attempt gets
	// new version IDs. Only this build's markers may be released by the live epoch.
	for _, d := range b.Data.Days {
		if _, err = tx.ExecContext(ctx, `UPDATE archive_day_versions SET state='abandoned' WHERE day_version_id=? AND state='building'`, archiveID(d.VersionID)); err != nil {
			return af.SealStatus{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE archive_days SET freeze_task_id=NULL,freeze_epoch=NULL,freeze_revision=NULL,freeze_until=NULL WHERE log_date=? AND freeze_task_id=? AND freeze_epoch<=?`, d.Date, archiveID(b.ID), g.WriterEpoch); err != nil {
			return af.SealStatus{}, err
		}
	}
	b.State, b.Error, b.Epoch = "blocked", code, g.WriterEpoch
	b.Progress++
	_, err = tx.ExecContext(ctx, `UPDATE archive_seal_builds SET state='blocked',error_code=?,writer_epoch=?,progress_version=?,updated_at=UTC_TIMESTAMP(6) WHERE build_id=?`, code, g.WriterEpoch, b.Progress, archiveID(b.ID))
	if err != nil {
		return af.SealStatus{}, err
	}
	if err = writerGuardBeforeCommit(ctx, tx, g); err != nil {
		return af.SealStatus{}, err
	}
	return b.status(g.WriterEpoch), tx.Commit()
}

func addSealFact(d *sealDay, f facts.Fact) error {
	h := sha256.New()
	if h.(encoding.BinaryUnmarshaler).UnmarshalBinary(d.FactState) != nil {
		return ErrWriterCheckpoint
	}
	writerHashField(h, f.FactHash[:])
	d.FactState, _ = h.(encoding.BinaryMarshaler).MarshalBinary()
	d.FactRows++
	if f.Quota != nil {
		n, ok := new(big.Int).SetString(d.FactQuota, 10)
		if !ok {
			return ErrWriterCheckpoint
		}
		d.FactQuota = n.Add(n, big.NewInt(*f.Quota)).String()
	}
	return nil
}
