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
	"sort"
	"strings"
	"time"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
)

func archiveVerifyBinding(ctx context.Context, tx *sql.Tx, site, dataset string, dates []string) (af.Dataset, af.CoveragePolicy, time.Time, error) {
	var empty af.Dataset
	var policy af.CoveragePolicy
	var now time.Time
	c, _, _, _, err := archiveRow(ctx, tx, site)
	if err != nil {
		return empty, policy, now, err
	}
	d, err := readArchiveDataset(ctx, tx, site, dataset, true)
	if err != nil {
		return empty, policy, now, err
	}
	var active []byte
	if err = tx.QueryRowContext(ctx, `SELECT active_dataset_id FROM site_log_archive_control WHERE site_id=?`, site).Scan(&active); err != nil {
		return empty, policy, now, err
	}
	if hex.EncodeToString(active) != dataset {
		return empty, policy, now, af.ErrConflict
	}
	if err = tx.QueryRowContext(ctx, `SELECT UTC_TIMESTAMP(6)`).Scan(&now); err != nil {
		return empty, policy, now, err
	}
	for _, date := range dates {
		start, err := archiveBackfillDate(date)
		if err != nil || start.AddDate(0, 0, 1).After(now.Add(-time.Duration(c.DelaySeconds)*time.Second)) {
			return empty, policy, now, af.ErrConflict
		}
	}
	p, err := archivePolicy(ctx, tx, dataset)
	return d, p.CoveragePolicy, now, err
}

// Request keys share the archive_tasks namespace, so a caller cannot turn one
// backfill request into a verification/seal request by reusing its identifier.
func archiveVerifyRequest(ctx context.Context, tx *sql.Tx, d af.Dataset, dates []string, kind, requestID string, binding any) ([]byte, []byte, string, error) {
	key := sha256.Sum256([]byte("manual:" + requestID))
	bound, err := json.Marshal(binding)
	if err != nil {
		return nil, nil, "", err
	}
	hash := sha256.Sum256([]byte(d.DatasetID + ":" + d.SourceGenerationID + ":" + kind + ":" + strings.Join(dates, ",") + ":" + string(bound)))
	var oldID, oldHash []byte
	err = tx.QueryRowContext(ctx, `SELECT task_id,request_hash FROM archive_tasks WHERE dataset_id=? AND request_key=?`, archiveIDBytes(d.DatasetID), key[:]).Scan(&oldID, &oldHash)
	if err == nil {
		if hex.EncodeToString(oldHash) != hex.EncodeToString(hash[:]) {
			return nil, nil, "", af.ErrConflict
		}
		return key[:], hash[:], hex.EncodeToString(oldID), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, "", err
	}
	return key[:], hash[:], "", nil
}

func archiveVerifyInsert(ctx context.Context, tx *sql.Tx, d af.Dataset, taskID string, dates []string, kind, actor string, key, hash []byte, parameters any, now time.Time) error {
	first, _ := archiveBackfillDate(dates[0])
	last, _ := archiveBackfillDate(dates[len(dates)-1])
	raw, err := json.Marshal(parameters)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO archive_tasks(task_id,dataset_id,source_generation_id,task_type,request_key,request_hash,from_unix,to_unix,status,parameters_json,attempt_no,requested_by,created_at,updated_at,log_date,next_attempt_at) VALUES(?,?,?,?,?,?,?,?,'queued',?,1,?,?,?,?,?)`, archiveIDBytes(taskID), archiveIDBytes(d.DatasetID), archiveIDBytes(d.SourceGenerationID), kind, key, hash, first.Unix(), last.AddDate(0, 0, 1).Unix(), string(raw), actor, now, now, dates[0], now)
	return err
}

// Every task owns an explicit, bounded set of Beijing dates. Sorting makes a
// retried HTTP request independent of the order in which its dates arrived.
func archiveVerifyDates(dates []string) ([]string, error) {
	if len(dates) == 0 || len(dates) > 31 {
		return nil, af.ErrConflict
	}
	out := append([]string(nil), dates...)
	sort.Strings(out)
	for i, date := range out {
		if _, err := archiveBackfillDate(date); err != nil || (i > 0 && date == out[i-1]) {
			return nil, af.ErrConflict
		}
	}
	return out, nil
}

func archiveVerificationScan(row archiveRowScanner) (ac.ArchiveTaskMetadata, []byte, []byte, string, int, error) {
	var item ac.ArchiveTaskMetadata
	var id, parameters, progress []byte
	var finished, next sql.NullTime
	var attempt int
	err := row.Scan(&id, &parameters, &item.State, &item.ErrorCode, &progress, &item.RequestedBy, &item.CreatedAt, &item.UpdatedAt, &finished, &next, &attempt)
	if errors.Is(err, sql.ErrNoRows) {
		err = af.ErrNotFound
	}
	if finished.Valid {
		item.FinishedAt = &finished.Time
	}
	if next.Valid {
		item.NextAttemptAt = &next.Time
	}
	return item, parameters, progress, hex.EncodeToString(id), attempt, err
}

func archiveScanReconcileTask(row archiveRowScanner) (ac.ReconcileTaskItem, error) {
	meta, parameters, progress, id, attempt, err := archiveVerificationScan(row)
	item := ac.ReconcileTaskItem{ArchiveTaskMetadata: meta}
	if err != nil {
		return item, err
	}
	if json.Unmarshal(parameters, &item.ReconcileTask) != nil {
		return item, af.ErrConflict
	}
	item.TaskID, item.Attempt = id, attempt
	if item.Validate() != nil {
		return item, af.ErrConflict
	}
	if len(progress) > 0 {
		item.Progress = new(af.ReconcileStatus)
		if json.Unmarshal(progress, item.Progress) != nil || item.Progress.Validate() != nil {
			return item, af.ErrConflict
		}
	}
	return item, nil
}

func archiveScanSealTask(row archiveRowScanner) (ac.SealTaskItem, error) {
	meta, parameters, progress, id, attempt, err := archiveVerificationScan(row)
	item := ac.SealTaskItem{ArchiveTaskMetadata: meta}
	if err != nil {
		return item, err
	}
	if json.Unmarshal(parameters, &item.SealTask) != nil {
		return item, af.ErrConflict
	}
	item.TaskID, item.Attempt = id, attempt
	if item.Validate() != nil {
		return item, af.ErrConflict
	}
	if len(progress) > 0 {
		item.Progress = new(af.SealStatus)
		if json.Unmarshal(progress, item.Progress) != nil || item.Progress.Validate() != nil {
			return item, af.ErrConflict
		}
	}
	return item, nil
}

func archiveVerificationActor(requestID, actor string) error {
	if _, err := af.IDBytes(requestID); err != nil || strings.TrimSpace(actor) == "" || len(actor) > 128 {
		return af.ErrConflict
	}
	return nil
}

func (s Store) CreateArchiveReconcile(ctx context.Context, site, dataset string, request ac.ReconcileRequest, actor string) (ac.ReconcileTaskItem, error) {
	var empty ac.ReconcileTaskItem
	if err := archiveVerificationActor(request.RequestID, actor); err != nil {
		return empty, err
	}
	dates, err := archiveVerifyDates([]string{request.Date})
	if err != nil {
		return empty, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	d, policy, now, err := archiveVerifyBinding(ctx, tx, site, dataset, dates)
	if err != nil {
		return empty, err
	}
	key, hash, id, err := archiveVerifyRequest(ctx, tx, d, dates, "day_verify", request.RequestID, request.Assurance)
	if err != nil {
		return empty, err
	}
	if id == "" {
		if request.Assurance.ValidUntilUnix <= now.Unix() || request.Assurance.StableBeforeUnix > now.Unix() {
			return empty, af.ErrConflict
		}
		var raw [16]byte
		if _, err = rand.Read(raw[:]); err != nil {
			return empty, err
		}
		id = hex.EncodeToString(raw[:])
		task := af.ReconcileTask{Identity: d.Identity, TaskID: id, Date: request.Date, Attempt: 1, Policy: policy, Assurance: request.Assurance}
		if task.Validate() != nil {
			return empty, af.ErrConflict
		}
		if err = archiveVerifyInsert(ctx, tx, d, id, dates, "day_verify", actor, key, hash, task, now); err != nil {
			return empty, err
		}
		if err = archiveBackfillAudit(ctx, tx, site, id, actor, "create_verification", request); err != nil {
			return empty, err
		}
	}
	item, err := archiveScanReconcileTask(tx.QueryRowContext(ctx, archiveTaskSelect+`WHERE task_id=? AND dataset_id=? AND task_type='day_verify'`, archiveIDBytes(id), archiveIDBytes(dataset)))
	if err != nil {
		return item, err
	}
	return item, tx.Commit()
}

func (s Store) CreateArchiveSeal(ctx context.Context, site, dataset string, request ac.SealRequest, actor string) (ac.SealTaskItem, error) {
	var empty ac.SealTaskItem
	if err := archiveVerificationActor(request.RequestID, actor); err != nil {
		return empty, err
	}
	dates, err := archiveVerifyDates(request.Dates)
	if err != nil {
		return empty, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	d, policy, now, err := archiveVerifyBinding(ctx, tx, site, dataset, dates)
	if err != nil {
		return empty, err
	}
	key, hash, id, err := archiveVerifyRequest(ctx, tx, d, dates, "day_seal", request.RequestID, nil)
	if err != nil {
		return empty, err
	}
	if id == "" {
		var raw [16]byte
		if _, err = rand.Read(raw[:]); err != nil {
			return empty, err
		}
		id = hex.EncodeToString(raw[:])
		task := af.SealTask{Identity: d.Identity, TaskID: id, Dates: dates, Attempt: 1, Policy: policy}
		if task.Validate() != nil {
			return empty, af.ErrConflict
		}
		if err = archiveVerifyInsert(ctx, tx, d, id, dates, "day_seal", actor, key, hash, task, now); err != nil {
			return empty, err
		}
		if err = archiveBackfillAudit(ctx, tx, site, id, actor, "create_seal", task); err != nil {
			return empty, err
		}
	}
	item, err := archiveScanSealTask(tx.QueryRowContext(ctx, archiveTaskSelect+`WHERE task_id=? AND dataset_id=? AND task_type='day_seal'`, archiveIDBytes(id), archiveIDBytes(dataset)))
	if err != nil {
		return item, err
	}
	return item, tx.Commit()
}

func (s Store) ListArchiveReconciles(ctx context.Context, site, dataset string) ([]ac.ReconcileTaskItem, error) {
	if _, err := s.GetArchiveDataset(ctx, site, dataset); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, archiveTaskSelect+`WHERE dataset_id=? AND task_type='day_verify' ORDER BY created_at DESC,task_id LIMIT 200`, archiveIDBytes(dataset))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ac.ReconcileTaskItem{}
	for rows.Next() {
		item, err := archiveScanReconcileTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s Store) ListArchiveSeals(ctx context.Context, site, dataset string) ([]ac.SealTaskItem, error) {
	if _, err := s.GetArchiveDataset(ctx, site, dataset); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, archiveTaskSelect+`WHERE dataset_id=? AND task_type='day_seal' ORDER BY created_at DESC,task_id LIMIT 200`, archiveIDBytes(dataset))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ac.SealTaskItem{}
	for rows.Next() {
		item, err := archiveScanSealTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func archiveVerificationRetry(ctx context.Context, tx *sql.Tx, id string, attempt int, parameters any) error {
	raw, err := json.Marshal(parameters)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE archive_tasks SET status='queued',parameters_json=?,attempt_no=?,lease_epoch=0,lease_session=NULL,progress_json=NULL,result_run_id=NULL,error_code=NULL,finished_at=NULL,next_attempt_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE task_id=?`, string(raw), attempt, archiveIDBytes(id))
	return err
}

func (s Store) RetryArchiveReconcile(ctx context.Context, site, dataset, taskID, actor string) (ac.ReconcileTaskItem, error) {
	var empty ac.ReconcileTaskItem
	if err := archiveVerificationActor(taskID, actor); err != nil {
		return empty, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	d, policy, now, err := archiveVerifyBinding(ctx, tx, site, dataset, nil)
	if err != nil {
		return empty, err
	}
	item, err := archiveScanReconcileTask(tx.QueryRowContext(ctx, archiveTaskSelect+`WHERE task_id=? AND dataset_id=? AND task_type='day_verify' FOR UPDATE`, archiveIDBytes(taskID), archiveIDBytes(dataset)))
	if err != nil {
		return empty, err
	}
	if !item.Identity.Equal(d.Identity) || (item.State != "blocked" && item.State != "retry_wait" && item.State != "mismatched") || item.Attempt == math.MaxInt32 || item.Assurance.ValidUntilUnix <= now.Unix() || (policy.SourceRetainedFrom != "" && item.Date < policy.SourceRetainedFrom) {
		return item, af.ErrConflict
	}
	item.Attempt++
	item.Policy = policy
	if err = archiveVerificationRetry(ctx, tx, taskID, item.Attempt, item.ReconcileTask); err != nil {
		return item, err
	}
	if err = archiveBackfillAudit(ctx, tx, site, taskID, actor, "retry_verification", map[string]any{"attempt": item.Attempt}); err != nil {
		return item, err
	}
	item, err = archiveScanReconcileTask(tx.QueryRowContext(ctx, archiveTaskSelect+`WHERE task_id=?`, archiveIDBytes(taskID)))
	if err != nil {
		return item, err
	}
	return item, tx.Commit()
}

func (s Store) RetryArchiveSeal(ctx context.Context, site, dataset, taskID, actor string) (ac.SealTaskItem, error) {
	var empty ac.SealTaskItem
	if err := archiveVerificationActor(taskID, actor); err != nil {
		return empty, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	d, policy, _, err := archiveVerifyBinding(ctx, tx, site, dataset, nil)
	if err != nil {
		return empty, err
	}
	item, err := archiveScanSealTask(tx.QueryRowContext(ctx, archiveTaskSelect+`WHERE task_id=? AND dataset_id=? AND task_type='day_seal' FOR UPDATE`, archiveIDBytes(taskID), archiveIDBytes(dataset)))
	if err != nil {
		return empty, err
	}
	if !item.Identity.Equal(d.Identity) || (item.State != "blocked" && item.State != "retry_wait") || item.Attempt == math.MaxInt32 {
		return item, af.ErrConflict
	}
	// A transiently paused target build may still own frozen dates. Its
	// automatic retry resumes the same attempt. Replacing it with a new
	// attempt here would orphan the only task able to release those markers.
	if item.State == "retry_wait" && item.Progress != nil && item.Progress.BuildID != "" {
		return item, af.ErrConflict
	}
	item.Attempt++
	item.Policy = policy
	if err = archiveVerificationRetry(ctx, tx, taskID, item.Attempt, item.SealTask); err != nil {
		return item, err
	}
	if err = archiveBackfillAudit(ctx, tx, site, taskID, actor, "retry_seal", map[string]any{"attempt": item.Attempt}); err != nil {
		return item, err
	}
	item, err = archiveScanSealTask(tx.QueryRowContext(ctx, archiveTaskSelect+`WHERE task_id=?`, archiveIDBytes(taskID)))
	if err != nil {
		return item, err
	}
	return item, tx.Commit()
}
