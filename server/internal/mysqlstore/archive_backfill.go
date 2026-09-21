package mysqlstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
)

var archiveBeijing = time.FixedZone("Beijing", 8*60*60)

func archiveBackfillDate(date string) (time.Time, error) {
	d, err := time.ParseInLocation("2006-01-02", date, archiveBeijing)
	if err != nil || d.Year() < 1970 || d.Year() > 9998 || d.Format("2006-01-02") != date {
		return time.Time{}, af.ErrConflict
	}
	return d, nil
}

func archivePolicy(ctx context.Context, q archiveDatasetQuerier, dataset string) (ac.ArchiveCoveragePolicy, error) {
	p := ac.ArchiveCoveragePolicy{CoveragePolicy: af.DefaultCoveragePolicy()}
	var raw []byte
	var from, retained sql.NullTime
	var actor sql.NullString
	var at sql.NullTime
	var revision uint64
	err := q.QueryRowContext(ctx, `SELECT coverage_from,source_retained_from,coverage_policy_json,coverage_policy_revision,coverage_confirmed_by,coverage_confirmed_at FROM archive_datasets WHERE dataset_id=?`, archiveIDBytes(dataset)).Scan(&from, &retained, &raw, &revision, &actor, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return p, af.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	if len(raw) > 0 && json.Unmarshal(raw, &p.CoveragePolicy) != nil {
		return p, af.ErrConflict
	}
	p.Revision = revision
	// Columns are authoritative for confirmed bounds and revision.
	if from.Valid {
		p.CoverageFrom = from.Time.Format("2006-01-02")
	}
	if retained.Valid {
		p.SourceRetainedFrom = retained.Time.Format("2006-01-02")
	}
	p.ConfirmedBy = actor.String
	if at.Valid {
		p.ConfirmedAt = &at.Time
	}
	if err := p.CoveragePolicy.Validate(); err != nil {
		return p, err
	}
	return p, nil
}

func (s Store) GetArchiveCoveragePolicy(ctx context.Context, site, dataset string) (ac.ArchiveCoveragePolicy, error) {
	if _, err := s.GetArchiveDataset(ctx, site, dataset); err != nil {
		return ac.ArchiveCoveragePolicy{}, err
	}
	return archivePolicy(ctx, s.db, dataset)
}

// Confirmation never derives a start date from currently surviving source rows.
func (s Store) UpdateArchiveCoveragePolicy(ctx context.Context, site, dataset string, p af.CoveragePolicy, actor string) (ac.ArchiveCoveragePolicy, error) {
	if p.Validate() != nil || p.CoverageFrom == "" || p.Evidence == "" || actor == "" || len(actor) > 128 || p.Revision == math.MaxUint64 {
		return ac.ArchiveCoveragePolicy{}, af.ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ac.ArchiveCoveragePolicy{}, err
	}
	defer tx.Rollback()
	d, err := readArchiveDataset(ctx, tx, site, dataset, true)
	if err != nil {
		return ac.ArchiveCoveragePolicy{}, err
	}
	old, err := archivePolicy(ctx, tx, dataset)
	if err != nil {
		return old, err
	}
	if old.Revision != p.Revision {
		return old, af.ErrConflict
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT UTC_TIMESTAMP(6)`).Scan(&now); err != nil {
		return old, err
	}
	if p.CoverageFrom > now.In(archiveBeijing).Format("2006-01-02") || p.SourceRetainedFrom > now.In(archiveBeijing).Format("2006-01-02") {
		return old, af.ErrConflict
	}
	p.Revision++
	raw, _ := json.Marshal(p)
	evidence, _ := json.Marshal(map[string]any{"evidence": p.Evidence, "confirmed_by": actor, "confirmed_at": now, "source_generation_id": d.SourceGenerationID})
	_, err = tx.ExecContext(ctx, `UPDATE archive_datasets SET coverage_from=?,source_retained_from=?,coverage_evidence_json=?,coverage_policy_json=?,coverage_policy_revision=?,coverage_confirmed_by=?,coverage_confirmed_at=?,updated_at=? WHERE dataset_id=?`, p.CoverageFrom, archiveNullableText(p.SourceRetainedFrom), string(evidence), string(raw), p.Revision, actor, now, now, archiveIDBytes(dataset))
	if err != nil {
		return old, err
	}
	if err = archiveBackfillAudit(ctx, tx, site, dataset, actor, "coverage_policy", p); err != nil {
		return old, err
	}
	return ac.ArchiveCoveragePolicy{CoveragePolicy: p, ConfirmedBy: actor, ConfirmedAt: &now}, tx.Commit()
}

func archiveBackfillAudit(ctx context.Context, tx *sql.Tx, site, id, actor, operation string, summary any) error {
	var instance string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM instances WHERE deleted=0 AND `+archiveSiteExpr+`=? ORDER BY id LIMIT 1`, site).Scan(&instance); err != nil {
		return err
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]any{"operation": operation, "value": summary})
	_, err := tx.ExecContext(ctx, `INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,before_summary,after_summary,status,created_at) VALUES(?,?,'archive.manage','archive_backfill',?,?,'{}',?,'success',UTC_TIMESTAMP(6))`, hex.EncodeToString(raw[:]), instance, id, actor, string(b))
	return err
}

const archiveTaskSelect = `SELECT task_id,parameters_json,status,COALESCE(error_code,''),progress_json,requested_by,created_at,updated_at,finished_at,next_attempt_at,attempt_no FROM archive_tasks `

type archiveRowScanner interface{ Scan(...any) error }

func archiveScanTask(row archiveRowScanner) (ac.BackfillTaskItem, error) {
	var item ac.BackfillTaskItem
	var id, params, progress []byte
	var finished, next sql.NullTime
	err := row.Scan(&id, &params, &item.State, &item.ErrorCode, &progress, &item.RequestedBy, &item.CreatedAt, &item.UpdatedAt, &finished, &next, &item.Attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return item, af.ErrNotFound
	}
	if err != nil {
		return item, err
	}
	attempt := item.Attempt
	if json.Unmarshal(params, &item.BackfillTask) != nil {
		return item, af.ErrConflict
	}
	item.TaskID, item.Attempt = hex.EncodeToString(id), attempt
	if len(progress) > 0 {
		var p af.BackfillStatus
		if json.Unmarshal(progress, &p) != nil {
			return item, af.ErrConflict
		}
		item.Progress = &p
	}
	if finished.Valid {
		item.FinishedAt = &finished.Time
	}
	if next.Valid {
		item.NextAttemptAt = &next.Time
	}
	return item, nil
}

func archiveNewTask(ctx context.Context, tx *sql.Tx, d af.Dataset, date, kind, key, actor string, p af.CoveragePolicy, now time.Time) (ac.BackfillTaskItem, error) {
	start, err := archiveBackfillDate(date)
	if err != nil {
		return ac.BackfillTaskItem{}, err
	}
	requestKey := sha256.Sum256([]byte(key))
	requestHash := sha256.Sum256([]byte(d.DatasetID + ":" + d.SourceGenerationID + ":" + kind + ":" + date))
	var storedHash []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash FROM archive_tasks WHERE dataset_id=? AND request_key=?`, archiveIDBytes(d.DatasetID), requestKey[:]).Scan(&storedHash)
	if err == nil {
		if hex.EncodeToString(storedHash) != hex.EncodeToString(requestHash[:]) {
			return ac.BackfillTaskItem{}, af.ErrConflict
		}
		return archiveScanTask(tx.QueryRowContext(ctx, archiveTaskSelect+`WHERE dataset_id=? AND request_key=?`, archiveIDBytes(d.DatasetID), requestKey[:]))
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ac.BackfillTaskItem{}, err
	}
	var raw [16]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return ac.BackfillTaskItem{}, err
	}
	task := af.BackfillTask{Identity: d.Identity, TaskID: hex.EncodeToString(raw[:]), Date: date, Type: kind, Attempt: 1, Policy: p}
	params, _ := json.Marshal(task)
	state, code := "queued", ""
	if p.SourceRetainedFrom != "" && date < p.SourceRetainedFrom {
		state, code = "blocked", "source_cleared"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO archive_tasks(task_id,dataset_id,source_generation_id,task_type,request_key,request_hash,from_unix,to_unix,status,parameters_json,attempt_no,requested_by,created_at,updated_at,log_date,next_attempt_at,error_code) VALUES(?,?,?,?,?,?,?,?,?,?,1,?,?,?,?,?,?)`, raw[:], archiveIDBytes(d.DatasetID), archiveIDBytes(d.SourceGenerationID), kind, requestKey[:], requestHash[:], start.Unix(), start.AddDate(0, 0, 1).Unix(), state, string(params), actor, now, now, date, now, archiveNullableText(code))
	if err != nil {
		return ac.BackfillTaskItem{}, err
	}
	return archiveScanTask(tx.QueryRowContext(ctx, archiveTaskSelect+`WHERE task_id=?`, raw[:]))
}

func (s Store) CreateArchiveBackfill(ctx context.Context, site, dataset string, r ac.BackfillRequest, actor string) (ac.BackfillTaskItem, error) {
	if _, err := af.IDBytes(r.RequestID); err != nil {
		return ac.BackfillTaskItem{}, af.ErrConflict
	}
	if _, err := archiveBackfillDate(r.Date); err != nil {
		return ac.BackfillTaskItem{}, err
	}
	if actor == "" || len(actor) > 128 {
		return ac.BackfillTaskItem{}, af.ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ac.BackfillTaskItem{}, err
	}
	defer tx.Rollback()
	// The site control row serializes task insertion with dispatch and pause.
	c, _, _, _, err := archiveRow(ctx, tx, site)
	if err != nil {
		return ac.BackfillTaskItem{}, err
	}
	d, err := readArchiveDataset(ctx, tx, site, dataset, true)
	if err != nil {
		return ac.BackfillTaskItem{}, err
	}
	var active []byte
	if err = tx.QueryRowContext(ctx, `SELECT active_dataset_id FROM site_log_archive_control WHERE site_id=?`, site).Scan(&active); err != nil {
		return ac.BackfillTaskItem{}, err
	}
	if hex.EncodeToString(active) != dataset {
		return ac.BackfillTaskItem{}, af.ErrConflict
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT UTC_TIMESTAMP(6)`).Scan(&now); err != nil {
		return ac.BackfillTaskItem{}, err
	}
	date, _ := archiveBackfillDate(r.Date)
	if date.AddDate(0, 0, 1).After(now.Add(-time.Duration(c.DelaySeconds) * time.Second)) {
		return ac.BackfillTaskItem{}, af.ErrConflict
	}
	p, err := archivePolicy(ctx, tx, dataset)
	if err != nil {
		return ac.BackfillTaskItem{}, err
	}
	item, err := archiveNewTask(ctx, tx, d, r.Date, "date_backfill", "manual:"+r.RequestID, actor, p.CoveragePolicy, now)
	if err != nil {
		return item, err
	}
	if err = archiveBackfillAudit(ctx, tx, site, item.TaskID, actor, "create", r); err != nil {
		return item, err
	}
	return item, tx.Commit()
}

func (s Store) ListArchiveBackfills(ctx context.Context, site, dataset string) ([]ac.BackfillTaskItem, error) {
	if _, err := s.GetArchiveDataset(ctx, site, dataset); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, archiveTaskSelect+`WHERE dataset_id=? AND task_type IN ('date_backfill','recent_backfill') ORDER BY created_at DESC,task_id LIMIT 200`, archiveIDBytes(dataset))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ac.BackfillTaskItem{}
	for rows.Next() {
		item, err := archiveScanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s Store) RetryArchiveBackfill(ctx context.Context, site, dataset, taskID, actor string) (ac.BackfillTaskItem, error) {
	if _, err := af.IDBytes(taskID); err != nil {
		return ac.BackfillTaskItem{}, err
	}
	if actor == "" || len(actor) > 128 {
		return ac.BackfillTaskItem{}, af.ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ac.BackfillTaskItem{}, err
	}
	defer tx.Rollback()
	if _, _, _, _, err = archiveRow(ctx, tx, site); err != nil {
		return ac.BackfillTaskItem{}, err
	}
	d, err := readArchiveDataset(ctx, tx, site, dataset, true)
	if err != nil {
		return ac.BackfillTaskItem{}, err
	}
	item, err := archiveScanTask(tx.QueryRowContext(ctx, archiveTaskSelect+`WHERE task_id=? AND dataset_id=? FOR UPDATE`, archiveIDBytes(taskID), archiveIDBytes(dataset)))
	if err != nil {
		return item, err
	}
	var active []byte
	if err = tx.QueryRowContext(ctx, `SELECT active_dataset_id FROM site_log_archive_control WHERE site_id=?`, site).Scan(&active); err != nil {
		return item, err
	}
	if hex.EncodeToString(active) != dataset {
		return item, af.ErrConflict
	}
	if item.SourceGenerationID != d.SourceGenerationID || (item.State != "blocked" && item.State != "retry_wait") || item.Attempt == math.MaxInt32 {
		return item, af.ErrConflict
	}
	p, err := archivePolicy(ctx, tx, dataset)
	if err != nil {
		return item, err
	}
	if p.SourceRetainedFrom != "" && item.Date < p.SourceRetainedFrom {
		return item, af.ErrConflict
	}
	item.Attempt++
	item.Policy = p.CoveragePolicy
	raw, _ := json.Marshal(item.BackfillTask)
	_, err = tx.ExecContext(ctx, `UPDATE archive_tasks SET status='queued',parameters_json=?,attempt_no=?,lease_epoch=0,lease_session=NULL,progress_json=NULL,error_code=NULL,finished_at=NULL,next_attempt_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE task_id=?`, string(raw), item.Attempt, archiveIDBytes(taskID))
	if err != nil {
		return item, err
	}
	if err = archiveBackfillAudit(ctx, tx, site, taskID, actor, "retry", map[string]any{"attempt": item.Attempt}); err != nil {
		return item, err
	}
	item, err = archiveScanTask(tx.QueryRowContext(ctx, archiveTaskSelect+`WHERE task_id=?`, archiveIDBytes(taskID)))
	if err != nil {
		return item, err
	}
	return item, tx.Commit()
}
