package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"strings"
	"time"
)

// Old complete files remain downloadable while replacements are in progress.
// Only discovery and generation progress disregard the previous generation.
const billingCurrentGenerationSQL = ` AND NOT EXISTS (SELECT 1 FROM billing_generation_ranges r WHERE r.instance_id=j.instance_id AND r.kind=j.job_type AND r.subject_id=(SELECT st.subject_id FROM billing_statement_jobs st WHERE st.job_id=j.id) AND r.cancelled=0 AND r.overwrite_existing=1 AND j.range_from>=CONVERT_TZ(r.range_from,'+08:00','+00:00') AND j.range_to<=CONVERT_TZ(r.range_to,'+08:00','+00:00') AND j.created_at<r.generation_started_at) `

func (s Store) BillingGenerationVersion(ctx context.Context, t billing.AutomaticTarget) (string, error) {
	var cancelled, overwrite bool
	var started sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT cancelled,overwrite_existing,generation_started_at FROM billing_generation_ranges WHERE instance_id=? AND kind=? AND subject_id=? AND range_from<=? AND range_to>? ORDER BY generation_started_at DESC LIMIT 1`, t.InstanceID, t.Kind, t.SubjectID, t.From.In(billing.BusinessLocation).Format("2006-01-02"), t.From.In(billing.BusinessLocation).Format("2006-01-02")).Scan(&cancelled, &overwrite, &started)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if cancelled {
		return "", billing.ErrGenerationCancelled
	}
	if overwrite && started.Valid {
		return started.Time.UTC().Format("20060102150405.000000"), nil
	}
	return "", nil
}

func (s Store) CancelBillingGeneration(ctx context.Context, targets []billing.AutomaticTarget) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range targets {
		if _, err = tx.ExecContext(ctx, `UPDATE billing_generation_ranges SET cancelled=1,last_error=NULL WHERE instance_id=? AND kind=? AND subject_id=? AND range_from=? AND range_to=?`, t.InstanceID, t.Kind, t.SubjectID, t.From.In(billing.BusinessLocation).Format("2006-01-02"), t.To.In(billing.BusinessLocation).Format("2006-01-02")); err != nil {
			return err
		}
		// Lock jobs before steps, matching the worker's publication lock order.
		if _, err = tx.ExecContext(ctx, `UPDATE billing_jobs j JOIN billing_statement_jobs st ON st.job_id=j.id SET j.status='failed',j.error_message='cancelled manually',j.finished_at=UTC_TIMESTAMP(6),j.updated_at=UTC_TIMESTAMP(6) WHERE j.instance_id=? AND j.job_type=? AND st.subject_id=? AND j.range_from>=? AND j.range_to<=? AND j.status IN ('pending','running','publishing')`, t.InstanceID, t.Kind, t.SubjectID, t.From.UTC(), t.To.UTC()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE billing_job_steps s JOIN billing_jobs j ON j.id=s.job_id JOIN billing_statement_jobs st ON st.job_id=j.id SET s.status='failed',s.error_message='cancelled manually',s.finished_at=UTC_TIMESTAMP(6),s.updated_at=UTC_TIMESTAMP(6) WHERE j.instance_id=? AND j.job_type=? AND st.subject_id=? AND j.range_from>=? AND j.range_to<=? AND j.error_message='cancelled manually' AND s.status IN ('pending','running')`, t.InstanceID, t.Kind, t.SubjectID, t.From.UTC(), t.To.UTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func supersedeBillingStatement(ctx context.Context, tx *sql.Tx, job billing.Job) error {
	if (job.JobType != "user_statement" && job.JobType != "upstream_statement") || job.UsageVersion < 3 || (job.BillPeriod != "daily" && job.BillPeriod != "monthly") {
		return nil
	}
	subject := billingJobSubject(job)
	if subject == 0 {
		if err := tx.QueryRowContext(ctx, `SELECT subject_id FROM billing_statement_jobs WHERE job_id=?`, job.ID).Scan(&subject); err != nil {
			return err
		}
	}
	err := supersedeBillingCandidates(ctx, tx, `j.instance_id=? AND j.job_type=? AND st.subject_id=? AND j.bill_period=? AND j.range_from=? AND j.range_to=? AND j.usage_version>=3 AND j.status IN ('complete','no_data') AND j.id<>?`, job.InstanceID, job.JobType, subject, job.BillPeriod, job.From.UTC(), job.To.UTC(), job.ID)
	if err != nil {
		return err
	}
	if job.BillPeriod == "daily" {
		// An overwrite keeps the previous month visible until a complete new
		// month is published. Ordinary incremental months still refresh per day.
		err = supersedeBillingCandidates(ctx, tx, `j.instance_id=? AND j.job_type=? AND st.subject_id=? AND j.bill_period='monthly' AND j.range_from<=? AND j.range_to>=? AND j.usage_version>=3 AND j.status IN ('complete','pending') AND NOT EXISTS (SELECT 1 FROM billing_month_daily_sources src WHERE src.month_job_id=j.id AND src.daily_job_id=?) AND NOT EXISTS (SELECT 1 FROM billing_generation_ranges r WHERE r.instance_id=j.instance_id AND r.kind=j.job_type AND r.subject_id=st.subject_id AND r.cancelled=0 AND r.overwrite_existing=1 AND j.created_at<r.generation_started_at AND j.range_from<CONVERT_TZ(r.range_to,'+08:00','+00:00') AND j.range_to>CONVERT_TZ(r.range_from,'+08:00','+00:00'))`, job.InstanceID, job.JobType, subject, job.From.UTC(), job.To.UTC(), job.ID)
	}
	return err
}

// Discover candidates with a non-locking read. A joined UPDATE can lock jobs
// for other subjects while scanning the site index, including their publishing
// rows. Point updates recheck eligibility using a current read, and never scan
// or lock unrelated subjects. The caller keeps replacement atomic with publish.
func supersedeBillingCandidates(ctx context.Context, tx *sql.Tx, predicate string, args ...any) error {
	rows, err := tx.QueryContext(ctx, `SELECT j.id FROM billing_jobs j JOIN billing_statement_jobs st ON st.job_id=j.id WHERE `+predicate+` ORDER BY j.id`, args...)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		// statement ownership is immutable. Its lookup is constrained to this job.
		updateArgs := append([]any{time.Now().UTC(), id}, args...)
		_, err = tx.ExecContext(ctx, `UPDATE billing_jobs j FORCE INDEX (PRIMARY) SET j.status='superseded',j.updated_at=? WHERE j.id=? AND `+strings.ReplaceAll(predicate, "st.subject_id", "(SELECT st.subject_id FROM billing_statement_jobs st WHERE st.job_id=j.id)"), updateArgs...)
		if err != nil {
			return err
		}
	}
	return nil
}

func billingJobSubject(job billing.Job) int64 {
	if job.JobType == "upstream_statement" {
		return job.UpstreamID
	}
	return job.UserID
}

// An explicit manual range wins over the overlapping automatic target. Jobs
// snapshot this choice at creation; retries never read mutable UI state.
func (s Store) BillingGenerationExcludeZeroOutput(ctx context.Context, t billing.AutomaticTarget) (bool, error) {
	var excluded bool
	err := s.db.QueryRowContext(ctx, `SELECT exclude_zero_output FROM billing_generation_ranges WHERE instance_id=? AND kind=? AND subject_id=? AND range_from<=? AND range_to>? ORDER BY generation_started_at DESC LIMIT 1`, t.InstanceID, t.Kind, t.SubjectID, t.From.In(billing.BusinessLocation).Format("2006-01-02"), t.From.In(billing.BusinessLocation).Format("2006-01-02")).Scan(&excluded)
	if err == sql.ErrNoRows {
		if t.To.IsZero() {
			return t.Kind == "upstream_statement", nil
		}
		return t.ExcludeZeroOutput, nil
	}
	return excluded, err
}
