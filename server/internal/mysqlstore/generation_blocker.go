package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"errors"
	"strings"
)

func (s Store) BillingGenerationBlocker(ctx context.Context, site string) (*billing.GenerationBlocker, error) {
	task, err := s.NextGeneration(ctx, site)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b := &billing.GenerationBlocker{Kind: task.Kind, Status: task.Status, Reason: task.Status}
	if task.Kind == "report" {
		err = s.db.QueryRowContext(ctx, `SELECT updated_at FROM settlement_report_tasks WHERE id=? AND instance_id=?`, task.ID, site).Scan(&b.UpdatedAt)
	} else {
		var message string
		err = s.db.QueryRowContext(ctx, `SELECT bill_period,error_message,updated_at,COALESCE((SELECT MAX(attempts) FROM billing_job_steps WHERE job_id=j.id),0) FROM billing_jobs j WHERE id=? AND instance_id=?`, task.ID, site).Scan(&b.Period, &message, &b.UpdatedAt, &b.Attempts)
		if strings.Contains(message, "3024") || strings.Contains(message, "maximum statement execution time exceeded") || strings.Contains(message, "source query timeout") {
			b.Reason = "source_timeout"
		} else if message != "" {
			b.Reason = "retrying"
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} // Task was cancelled/deleted between reads.
	return b, err
}
