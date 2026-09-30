package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"encoding/json"
)

func (s Store) billingMonthlyCoverage(ctx context.Context, job billing.Job) (*billing.MonthlyCoverage, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT coverage_json FROM billing_month_coverage WHERE job_id=?`, job.ID).Scan(&raw)
	if err == nil {
		var coverage billing.MonthlyCoverage
		err = json.Unmarshal([]byte(raw), &coverage)
		return &coverage, err
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	// Never infer historical coverage from today's daily jobs or empty checks.
	// Those could have been produced after this monthly snapshot was frozen.
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT DATE_FORMAT(bill_day,'%Y-%m-%d') FROM billing_compact_daily_totals WHERE job_id=?`, job.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := map[string]bool{}
	for rows.Next() {
		var day string
		if err = rows.Scan(&day); err != nil {
			return nil, err
		}
		days[day] = false
	}
	return billing.NewMonthlyCoverage(job.From, job.To, days), rows.Err()
}

func snapshotBillingMonthCoverage(ctx context.Context, tx *sql.Tx, job billing.Job, days map[string]bool) error {
	// Read CT's completed checks only. Unchecked, ongoing and future dates stay
	// missing. An overwrite generation invalidates older empty-day evidence.
	rows, err := tx.QueryContext(ctx, `SELECT DATE_FORMAT(c.bill_day,'%Y-%m-%d') FROM billing_day_checks c WHERE c.instance_id=? AND c.kind=? AND c.subject_id=? AND c.has_consumption=0 AND c.bill_day>=? AND c.bill_day<? AND c.checked_at<=? AND c.checked_at>=CONVERT_TZ(DATE_ADD(c.bill_day,INTERVAL 1 DAY),'+08:00','+00:00') AND NOT EXISTS (SELECT 1 FROM billing_generation_ranges r WHERE r.instance_id=c.instance_id AND r.kind=c.kind AND r.subject_id=c.subject_id AND r.cancelled=0 AND r.overwrite_existing=1 AND r.range_from<=c.bill_day AND r.range_to>c.bill_day AND r.generation_started_at>c.checked_at)`, job.InstanceID, job.JobType, billingJobSubject(job), job.From.In(billing.BusinessLocation).Format("2006-01-02"), job.To.In(billing.BusinessLocation).Format("2006-01-02"), job.CreatedAt.UTC())
	if err != nil {
		return err
	}
	for rows.Next() {
		var day string
		if err = rows.Scan(&day); err != nil {
			rows.Close()
			return err
		}
		if _, exists := days[day]; !exists {
			days[day] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(billing.NewMonthlyCoverage(job.From, job.To, days))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO billing_month_coverage(job_id,coverage_json) VALUES(?,?)`, job.ID, string(raw))
	return err
}
