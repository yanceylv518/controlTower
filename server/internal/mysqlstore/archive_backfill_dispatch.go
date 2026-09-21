package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
)

// Called with the site's control row locked. A task is only useful alongside a
// target-claimed P2 grant; zero-epoch bootstrap and paused polls cannot dispatch.
func archiveBackfillPoll(ctx context.Context, tx *sql.Tx, out *ac.Response, st ac.Status, now time.Time) error {
	if out.Config.FullHistory { return nil }
	if !out.Granted || out.WriterGrant == nil || st.Foundation == nil || (!st.Foundation.SupportsBackfill() && !st.Foundation.SupportsReconcile() && !st.Foundation.SupportsSeal()) {
		return nil
	}
	p, err := archivePolicy(ctx, tx, st.Foundation.DatasetID)
	if err != nil {
		return err
	}
	out.ArchivePolicy = &p.CoveragePolicy
	if st.Foundation.WriterEpoch != out.WriterGrant.WriterEpoch {
		return nil
	}
	if st.Backfill != nil && st.Foundation.SupportsBackfill() {
		accepted, err := archiveAcceptBackfill(ctx, tx, *out.WriterGrant, *st.Backfill, now)
		if err != nil {
			return err
		}
		out.BackfillAccepted = accepted
	}
	if st.Reconcile != nil && st.Foundation.SupportsReconcile() {
		accepted, err := archiveAcceptReconcile(ctx, tx, *out.WriterGrant, *st.Reconcile, now)
		if err != nil {
			return err
		}
		out.ReconcileAccepted = accepted
	}
	if st.Seal != nil && st.Foundation.SupportsSeal() {
		accepted, err := archiveAcceptSeal(ctx, tx, *out.WriterGrant, *st.Seal, now)
		if err != nil {
			return err
		}
		out.SealAccepted = accepted
	}
	d, err := readArchiveDataset(ctx, tx, out.SiteID, st.Foundation.DatasetID, false)
	if err != nil {
		return err
	}
	if !d.Identity.Equal(st.Foundation.Identity) {
		return af.ErrIdentity
	}
	var kinds []string
	if st.Foundation.SupportsBackfill() {
		if err = archiveScheduleRecent(ctx, tx, d, p.CoveragePolicy, out.Config.DelaySeconds, now); err != nil {
			return err
		}
		kinds = append(kinds, "'date_backfill'", "'recent_backfill'")
	}
	if st.Foundation.SupportsReconcile() {
		kinds = append(kinds, "'day_verify'")
	}
	if st.Foundation.SupportsSeal() {
		kinds = append(kinds, "'day_seal'")
	}
	// An in-flight task always resumes first, even after its CT lease expires.
	// Ready manual jobs share FIFO ordering; recent scans gain priority after
	// thirty minutes. A retry's future deadline frees the writer for other work.
	var taskID []byte
	var kind string
	err = tx.QueryRowContext(ctx, `SELECT task_id,task_type FROM archive_tasks WHERE dataset_id=? AND source_generation_id=? AND task_type IN (`+strings.Join(kinds, ",")+`) AND (status='running' OR (status IN ('queued','retry_wait') AND next_attempt_at<=?)) ORDER BY CASE WHEN status='running' THEN 0 WHEN task_type='recent_backfill' AND created_at<=? THEN 1 WHEN task_type='recent_backfill' THEN 3 ELSE 2 END,created_at,task_id LIMIT 1 FOR UPDATE`, archiveIDBytes(d.DatasetID), archiveIDBytes(d.SourceGenerationID), now, now.Add(-30*time.Minute)).Scan(&taskID, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	switch kind {
	case "day_verify":
		item, err := archiveScanReconcileTask(tx.QueryRowContext(ctx, archiveTaskSelect+`WHERE task_id=?`, taskID))
		if err != nil {
			return err
		}
		if p.SourceRetainedFrom != "" && item.Date < p.SourceRetainedFrom {
			_, err = tx.ExecContext(ctx, `UPDATE archive_tasks SET status='blocked',error_code='source_cleared',updated_at=?,finished_at=? WHERE task_id=?`, now, now, taskID)
			return err
		}
		if !item.Assurance.Covers(item.Date, now.Unix()) {
			_, err = tx.ExecContext(ctx, `UPDATE archive_tasks SET status='blocked',error_code='verification_expired',updated_at=?,finished_at=? WHERE task_id=?`, now, now, taskID)
			return err
		}
		out.ReconcileTask = &item.ReconcileTask
	case "day_seal":
		item, err := archiveScanSealTask(tx.QueryRowContext(ctx, archiveTaskSelect+`WHERE task_id=?`, taskID))
		if err != nil {
			return err
		}
		out.SealTask = &item.SealTask
	default:
		item, err := archiveScanTask(tx.QueryRowContext(ctx, archiveTaskSelect+`WHERE task_id=?`, taskID))
		if err != nil {
			return err
		}
		if p.SourceRetainedFrom != "" && item.Date < p.SourceRetainedFrom {
			_, err = tx.ExecContext(ctx, `UPDATE archive_tasks SET status='blocked',error_code='source_cleared',updated_at=?,finished_at=? WHERE task_id=?`, now, now, taskID)
			return err
		}
		out.BackfillTask = &item.BackfillTask
	}
	_, err = tx.ExecContext(ctx, `UPDATE archive_tasks SET status='running',lease_epoch=?,lease_session=?,dispatched_at=COALESCE(dispatched_at,?),updated_at=?,next_attempt_at=NULL WHERE task_id=?`, out.WriterGrant.WriterEpoch, st.Session, now, now, taskID)
	if err != nil {
		return err
	}
	return nil
}

func archiveScheduleRecent(ctx context.Context, tx *sql.Tx, d af.Dataset, p af.CoveragePolicy, delay int, now time.Time) error {
	today := now.In(archiveBeijing)
	for age := 1; age <= p.RecentDays; age++ {
		date := today.AddDate(0, 0, -age).Format("2006-01-02")
		start, _ := archiveBackfillDate(date)
		if start.AddDate(0, 0, 1).After(now.Add(-time.Duration(delay) * time.Second)) {
			continue
		}
		if p.CoverageFrom != "" && date < p.CoverageFrom {
			continue
		}
		if p.SourceRetainedFrom != "" && date < p.SourceRetainedFrom {
			continue
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM archive_tasks WHERE dataset_id=? AND log_date=? AND task_type IN ('date_backfill','recent_backfill') AND (status IN ('queued','running','retry_wait') OR created_at>?)`, archiveIDBytes(d.DatasetID), date, now.Add(-6*time.Hour)).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			continue
		}
		key := fmt.Sprintf("recent:%s:%d", date, now.Unix()/(6*60*60))
		if _, err := archiveNewTask(ctx, tx, d, date, "recent_backfill", key, "scheduler", p, now); err != nil {
			return err
		}
	}
	return nil
}

func archiveAcceptBackfill(ctx context.Context, tx *sql.Tx, grant af.WriterGrant, report af.BackfillStatus, now time.Time) (bool, error) {
	if report.Validate() != nil || report.WriterEpoch != grant.WriterEpoch {
		return false, nil
	}
	var dataset, generation []byte
	var epoch uint64
	var session string
	var state string
	var attempt int
	var date time.Time
	var previous []byte
	err := tx.QueryRowContext(ctx, `SELECT dataset_id,source_generation_id,lease_epoch,COALESCE(lease_session,''),status,attempt_no,log_date,progress_json FROM archive_tasks WHERE task_id=? FOR UPDATE`, archiveIDBytes(report.TaskID)).Scan(&dataset, &generation, &epoch, &session, &state, &attempt, &date, &previous)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if hex.EncodeToString(dataset) != grant.DatasetID || hex.EncodeToString(generation) != grant.SourceGenerationID || epoch != grant.WriterEpoch || session != grant.Session || attempt != report.Attempt || date.Format("2006-01-02") != report.Date {
		return false, nil
	}
	var old af.BackfillStatus
	if len(previous) > 0 && json.Unmarshal(previous, &old) != nil {
		return false, af.ErrConflict
	}
	if len(previous) > 0 && old == report {
		return true, nil
	}
	if len(previous) > 0 && (report.ScannedRows < old.ScannedRows || report.AfterCreatedUnix < old.AfterCreatedUnix || (report.AfterCreatedUnix == old.AfterCreatedUnix && report.AfterID < old.AfterID) || report.CatalogRevision < old.CatalogRevision) {
		return false, nil
	}
	if state != "running" {
		// Lost poll responses may replay a terminal result. Acknowledge only
		// the same result; never let it replace a newer scan or task state.
		return len(previous) > 0 && old == report, nil
	}
	raw, _ := json.Marshal(report)
	var next, finished any
	if report.State == "retry_wait" {
		// The target owns its retry budget. Keep a long target backoff out
		// of the running queue so other dates can use the writer meanwhile.
		// CT time bounds untrusted, expired, or clock-skewed timestamps.
		deadline := now.Add(30 * time.Second)
		ceiling := now.Add(900 * time.Second)
		if report.RetryAfterUnix > ceiling.Unix() {
			deadline = ceiling
		} else if report.RetryAfterUnix > deadline.Unix() {
			deadline = time.Unix(report.RetryAfterUnix, 0)
		}
		next = deadline
	}
	if report.State == "succeeded" || report.State == "blocked" {
		finished = now
	}
	_, err = tx.ExecContext(ctx, `UPDATE archive_tasks SET status=?,progress_json=?,error_code=?,next_attempt_at=?,finished_at=?,updated_at=? WHERE task_id=?`, report.State, string(raw), archiveNullableText(report.ErrorCode), next, finished, now, archiveIDBytes(report.TaskID))
	return err == nil, err
}
