package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	af "controltower/internal/archivecontract"
)

type archiveVerificationLease struct {
	state                string
	parameters, previous []byte
}

func archiveVerificationReportLease(ctx context.Context, tx *sql.Tx, grant af.WriterGrant, id, kind string, attempt int, epoch uint64) (archiveVerificationLease, bool, error) {
	var item archiveVerificationLease
	var dataset, generation []byte
	var storedEpoch uint64
	var session string
	var storedAttempt int
	err := tx.QueryRowContext(ctx, `SELECT dataset_id,source_generation_id,lease_epoch,COALESCE(lease_session,''),status,attempt_no,parameters_json,progress_json FROM archive_tasks WHERE task_id=? AND task_type=? FOR UPDATE`, archiveIDBytes(id), kind).Scan(&dataset, &generation, &storedEpoch, &session, &item.state, &storedAttempt, &item.parameters, &item.previous)
	if errors.Is(err, sql.ErrNoRows) {
		return item, false, nil
	}
	if err != nil {
		return item, false, err
	}
	bound := hex.EncodeToString(dataset) == grant.DatasetID && hex.EncodeToString(generation) == grant.SourceGenerationID && storedEpoch == grant.WriterEpoch && epoch == grant.WriterEpoch && session == grant.Session && storedAttempt == attempt
	return item, bound, nil
}

func archiveAcceptReconcile(ctx context.Context, tx *sql.Tx, grant af.WriterGrant, report af.ReconcileStatus, now time.Time) (bool, error) {
	if report.Validate() != nil {
		return false, nil
	}
	item, bound, err := archiveVerificationReportLease(ctx, tx, grant, report.TaskID, "day_verify", report.Attempt, report.WriterEpoch)
	if err != nil || !bound {
		return false, err
	}
	var task af.ReconcileTask
	if json.Unmarshal(item.parameters, &task) != nil || !task.Identity.Equal(grant.Identity) {
		return false, af.ErrConflict
	}
	if task.Date != report.Date {
		return false, nil
	}
	var old af.ReconcileStatus
	if len(item.previous) > 0 {
		if json.Unmarshal(item.previous, &old) != nil {
			return false, af.ErrConflict
		}
		if old == report {
			return true, nil
		}
		if report.ProgressVersion < old.ProgressVersion || (report.ProgressVersion == old.ProgressVersion && !archiveReconcileOperationalRetry(old, report)) || report.CatalogRevision < old.CatalogRevision || (old.RunID != "" && report.RunID != old.RunID) {
			return false, nil
		}
	}
	if item.state != "running" {
		return false, nil
	}
	// A result is an archive run reference, not caller-supplied billing proof.
	// The independent Server reader is the only path that updates the catalog.
	return archiveSaveVerificationReport(ctx, tx, report.TaskID, report.RunID, report.State, report.ErrorCode, report.RetryAfterUnix, report.State == "matched" || report.State == "mismatched" || report.State == "blocked", report, now)
}

func archiveAcceptSeal(ctx context.Context, tx *sql.Tx, grant af.WriterGrant, report af.SealStatus, now time.Time) (bool, error) {
	if report.Validate() != nil {
		return false, nil
	}
	item, bound, err := archiveVerificationReportLease(ctx, tx, grant, report.TaskID, "day_seal", report.Attempt, report.WriterEpoch)
	if err != nil || !bound {
		return false, err
	}
	var task af.SealTask
	if json.Unmarshal(item.parameters, &task) != nil || !task.Identity.Equal(grant.Identity) {
		return false, af.ErrConflict
	}
	if report.State == "succeeded" {
		if len(report.Versions) != len(task.Dates) {
			return false, nil
		}
		for i, day := range report.Versions {
			if day.Date != task.Dates[i] {
				return false, nil
			}
		}
	}
	var old af.SealStatus
	if len(item.previous) > 0 {
		if json.Unmarshal(item.previous, &old) != nil {
			return false, af.ErrConflict
		}
		if reflect.DeepEqual(old, report) {
			return true, nil
		}
		if report.ProgressVersion < old.ProgressVersion || (report.ProgressVersion == old.ProgressVersion && !archiveSealOperationalRetry(old, report)) || report.CatalogRevision < old.CatalogRevision || (old.BuildID != "" && report.BuildID != old.BuildID) {
			return false, nil
		}
	}
	if item.state != "running" {
		return false, nil
	}
	return archiveSaveVerificationReport(ctx, tx, report.TaskID, report.BuildID, report.State, report.ErrorCode, report.RetryAfterUnix, report.State == "succeeded" || report.State == "blocked", report, now)
}

// A transport error cannot increment the target's persisted evidence version.
// The Agent may report a control-plane retry at the same version only when all
// target progress is byte-for-byte unchanged. This never authorizes a match or
// publication. The surrounding lease/state guards prevent early redispatch.
func archiveReconcileOperationalRetry(old, next af.ReconcileStatus) bool {
	if old.State != "running" || next.State != "retry_wait" {
		return false
	}
	next.State, next.ErrorCode, next.RetryAfterUnix, next.WriterEpoch = old.State, old.ErrorCode, old.RetryAfterUnix, old.WriterEpoch
	return old == next
}

func archiveSealOperationalRetry(old, next af.SealStatus) bool {
	if old.State != "running" || next.State != "retry_wait" {
		return false
	}
	next.State, next.ErrorCode, next.RetryAfterUnix, next.WriterEpoch = old.State, old.ErrorCode, old.RetryAfterUnix, old.WriterEpoch
	return reflect.DeepEqual(old, next)
}

func archiveSaveVerificationReport(ctx context.Context, tx *sql.Tx, id, run, state, code string, retry int64, terminal bool, report any, now time.Time) (bool, error) {
	raw, err := json.Marshal(report)
	if err != nil {
		return false, err
	}
	var next, finished, runID any
	if run != "" {
		runID = archiveIDBytes(run)
	}
	if terminal {
		finished = now
	}
	if state == "retry_wait" {
		deadline := now.Add(30 * time.Second)
		ceiling := now.Add(900 * time.Second)
		if retry > ceiling.Unix() {
			deadline = ceiling
		} else if retry > deadline.Unix() {
			deadline = time.Unix(retry, 0)
		}
		next = deadline
	}
	_, err = tx.ExecContext(ctx, `UPDATE archive_tasks SET status=?,progress_json=?,result_run_id=?,error_code=?,next_attempt_at=?,finished_at=?,updated_at=? WHERE task_id=?`, state, string(raw), runID, archiveNullableText(code), next, finished, now, archiveIDBytes(id))
	return err == nil, err
}
