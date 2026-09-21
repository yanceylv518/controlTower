package archivereader

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	af "controltower/internal/archivecontract"
)

// ReadDayVerification exposes bounded operational evidence and published
// manifests only. The restricted reader never opens raw logs or payload tables.
func (r Reader) ReadDayVerification(ctx context.Context, registration af.Registration, date, runID string, afterID int64) (af.DayVerification, error) {
	out := af.DayVerification{Identity: registration.Identity, Date: date, Runs: []af.VerificationRun{}, Issues: []af.VerificationIssue{}, Versions: []af.PublishedDayVersion{}, DayState: "unknown"}
	if registration.Validate() != nil || afterID < 0 || (afterID > 0 && runID == "") {
		return out, af.ErrConflict
	}
	if _, _, err := af.DateBounds(date); err != nil {
		return out, err
	}
	if runID != "" {
		if _, err := af.IDBytes(runID); err != nil {
			return out, err
		}
	}
	db, err := r.open(ctx, registration.StorageRef)
	if err != nil {
		return out, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, ErrUnavailable
	}
	defer tx.Rollback()
	if err = verifyRegistration(ctx, tx, registration); err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `SELECT state FROM archive_days WHERE log_date=?`, date).Scan(&out.DayState)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, ErrUnavailable
	}
	query := `SELECT run_id,task_id,attempt,state,phase,method,start_revision,final_revision,issue_count,summary_json,assurance_json,started_at,completed_at,error_code FROM archive_reconcile_runs WHERE log_date=?`
	args := []any{date}
	if runID != "" {
		query += " AND run_id=?"
		id, _ := af.IDBytes(runID)
		args = append(args, id)
	}
	query += " ORDER BY started_at DESC,run_id DESC LIMIT 100"
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return out, ErrUnavailable
	}
	for rows.Next() {
		var v af.VerificationRun
		var id, task, assurance []byte
		var rev sql.Null[uint64]
		var ended sql.NullTime
		var code sql.NullString
		if rows.Scan(&id, &task, &v.Attempt, &v.State, &v.Phase, &v.Method, &v.StartRevision, &rev, &v.IssueCount, &v.Summary, &assurance, &v.StartedAt, &ended, &code) != nil || len(id) != 16 || len(task) != 16 || json.Unmarshal(assurance, &v.Assurance) != nil {
			rows.Close()
			return out, af.ErrConflict
		}
		v.RunID, v.TaskID = hex.EncodeToString(id), hex.EncodeToString(task)
		if rev.Valid {
			v.FinalRevision = &rev.V
		}
		if ended.Valid {
			v.CompletedAt = &ended.Time
		}
		v.ErrorCode = code.String
		out.Runs = append(out.Runs, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, ErrUnavailable
	}
	if runID != "" && len(out.Runs) == 0 {
		return out, af.ErrNotFound
	}
	if len(out.Runs) > 0 {
		out.SelectedRunID = out.Runs[0].RunID
		id, _ := af.IDBytes(out.SelectedRunID)
		rows, err = tx.QueryContext(ctx, `SELECT source_id,issue_kind,source_row_hash,target_row_hash FROM archive_reconcile_issues WHERE run_id=? AND issue_kind<>'equal' AND source_id>? ORDER BY source_id LIMIT 201`, id, afterID)
		if err != nil {
			return out, ErrUnavailable
		}
		for rows.Next() {
			var v af.VerificationIssue
			var source, target []byte
			if rows.Scan(&v.SourceID, &v.Kind, &source, &target) != nil {
				rows.Close()
				return out, ErrUnavailable
			}
			v.SourceHash, v.TargetHash = hex.EncodeToString(source), hex.EncodeToString(target)
			if len(out.Issues) == 200 {
				next := out.Issues[199].SourceID
				out.NextIssueID = &next
				break
			}
			out.Issues = append(out.Issues, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, ErrUnavailable
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT v.day_version_id,v.version_no,v.manifest_hash,v.verified_mutation_revision,v.publish_revision,v.published_at,v.manifest_json,COALESCE(d.current_version_id=v.day_version_id,0),v.reconcile_run_id FROM archive_day_versions v LEFT JOIN archive_days d ON d.log_date=v.log_date WHERE v.log_date=? AND v.state='published' ORDER BY v.version_no DESC LIMIT 100`, date)
	if err != nil {
		return out, ErrUnavailable
	}
	for rows.Next() {
		var v af.PublishedDayVersion
		var id, hash, evidenceRun []byte
		v.Date = date
		if rows.Scan(&id, &v.VersionNo, &hash, &v.MutationRevision, &v.PublishRevision, &v.PublishedAt, &v.Manifest, &v.IsCurrent, &evidenceRun) != nil {
			rows.Close()
			return out, ErrUnavailable
		}
		v.VersionID, v.ManifestHash = hex.EncodeToString(id), hex.EncodeToString(hash)
		actual, e := af.ManifestHash(v.Manifest)
		if e != nil || actual != v.ManifestHash || len(id) != 16 || len(evidenceRun) != 16 {
			rows.Close()
			return out, af.ErrConflict
		}
		var binding struct {
			Identity         af.Identity `json:"identity"`
			Date             string      `json:"date"`
			VersionID        string      `json:"version_id"`
			RunID            string      `json:"run_id"`
			VersionNo        uint64      `json:"version_no,string"`
			MutationRevision uint64      `json:"mutation_revision,string"`
			PublishRevision  uint64      `json:"publish_revision,string"`
		}
		if json.Unmarshal(v.Manifest, &binding) != nil || !binding.Identity.Equal(registration.Identity) || binding.Date != date || binding.VersionID != v.VersionID || binding.RunID != hex.EncodeToString(evidenceRun) || binding.VersionNo != v.VersionNo || binding.MutationRevision != v.MutationRevision || binding.PublishRevision != v.PublishRevision {
			rows.Close()
			return out, af.ErrConflict
		}
		out.Versions = append(out.Versions, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, ErrUnavailable
	}
	return out, tx.Commit()
}

func verifyRegistration(ctx context.Context, tx *sql.Tx, r af.Registration) error {
	var site string
	var dataset, generation, schema, source []byte
	var format, count int
	if tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM archive_dataset_meta").Scan(&count) != nil || count != 1 {
		return af.ErrIdentity
	}
	if tx.QueryRowContext(ctx, `SELECT site_id,dataset_id,source_generation_id,format_version,schema_fingerprint,source_identity_hash FROM archive_dataset_meta WHERE singleton_id=1`).Scan(&site, &dataset, &generation, &format, &schema, &source) != nil {
		return ErrUnavailable
	}
	if site != r.SiteID || hex.EncodeToString(dataset) != r.DatasetID || hex.EncodeToString(generation) != r.SourceGenerationID || hex.EncodeToString(schema) != r.SchemaFingerprint || hex.EncodeToString(source) != r.SourceFingerprint {
		return af.ErrIdentity
	}
	if format != af.FormatVersion {
		return af.ErrUnsupported
	}
	return nil
}
