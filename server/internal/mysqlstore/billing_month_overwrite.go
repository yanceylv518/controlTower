package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"time"
)

// Manual overwrite is a replacement of the selected range, not an incremental
// month refresh. Wait for every required day before either publishing a new
// month or accepting a verified empty replacement. Open-month tasks freeze their
// work_until, so future days never hold an already submitted overwrite open.
func checkBillingMonthOverwriteCoverage(ctx context.Context, tx *sql.Tx, job billing.Job, completed map[string]bool) error {
	rows, err := tx.QueryContext(ctx, `SELECT r.range_from,r.range_to,r.generation_started_at,t.work_until FROM billing_generation_ranges r LEFT JOIN billing_generation_tasks t ON t.id=r.batch_id WHERE r.instance_id=? AND r.kind=? AND r.subject_id=? AND r.cancelled=0 AND r.overwrite_existing=1 AND r.range_from<? AND r.range_to>?`, job.InstanceID, job.JobType, billingJobSubject(job), job.To.In(billing.BusinessLocation).Format("2006-01-02"), job.From.In(billing.BusinessLocation).Format("2006-01-02"))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var from, to, started time.Time
		var workUntil sql.NullTime
		if err = rows.Scan(&from, &to, &started, &workUntil); err != nil {
			return err
		}
		if job.CreatedAt.Before(started) {
			return billing.ErrGenerationCancelled
		}
		from, err = time.ParseInLocation("2006-01-02", from.Format("2006-01-02"), billing.BusinessLocation)
		if err != nil {
			return err
		}
		to, err = time.ParseInLocation("2006-01-02", to.Format("2006-01-02"), billing.BusinessLocation)
		if err != nil {
			return err
		}
		if from.Before(job.From) {
			from = job.From
		}
		for _, end := range []time.Time{job.To, billing.CompleteDayBoundary(job.CreatedAt)} {
			if to.After(end) {
				to = end
			}
		}
		if workUntil.Valid && to.After(workUntil.Time) {
			to = workUntil.Time
		}
		for day := from; day.Before(to); day = day.AddDate(0, 0, 1) {
			if _, ok := completed[day.In(billing.BusinessLocation).Format("2006-01-02")]; !ok {
				return billing.ErrDailyBillsIncomplete
			}
		}
	}
	return rows.Err()
}
