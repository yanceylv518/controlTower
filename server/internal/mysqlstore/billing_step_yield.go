package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
)

// AppendBillingHour has already committed aggregates, cursor and spool lineage.
// A voluntary yield is not a failed attempt. Keep job running so ordinary day
// arrivals do not reorder it; only monthly publication can run in between.
func (s Store) YieldBillingStep(ctx context.Context, job billing.Job, step billing.JobStep) error {
	_, err := s.db.ExecContext(ctx, `UPDATE billing_job_steps s JOIN billing_jobs j ON j.id=s.job_id SET s.status='pending',s.attempts=GREATEST(0,s.attempts-1),s.error_message='',j.error_message='',s.updated_at=UTC_TIMESTAMP(6),j.updated_at=UTC_TIMESTAMP(6) WHERE s.job_id=? AND s.step_no=? AND j.instance_id=? AND j.status='running' AND s.status='running'`, job.ID, step.StepNo, job.InstanceID)
	return err
}
