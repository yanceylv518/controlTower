package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"encoding/json"
	"fmt"
)

func bindBillingMoneySnapshot(ctx context.Context, tx *sql.Tx, job billing.Job) error {
	if job.MoneySnapshot == nil {
		return nil
	} // Existing legacy callers remain distinguishable.
	s := job.MoneySnapshot
	if err := s.Validate(job.InstanceID); err != nil {
		return err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO billing_money_snapshots(id,instance_id,observed_at,snapshot_json) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE id=id`, s.ID, s.SiteID, s.ObservedAt, string(raw)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO billing_job_money_snapshots(job_id,snapshot_id) VALUES(?,?)`, job.ID, s.ID)
	return err
}

func (s Store) FailedStatementMoneySnapshot(ctx context.Context, key string) (*billing.MoneySnapshot, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM billing_jobs WHERE request_key=? AND status='failed'`, key).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.BillingJobMoneySnapshot(ctx, id)
}

func (s Store) BillingJobMoneySnapshot(ctx context.Context, jobID string) (*billing.MoneySnapshot, error) {
	var raw, site, id string
	err := s.db.QueryRowContext(ctx, `SELECT m.snapshot_json,j.instance_id,m.id FROM billing_job_money_snapshots b JOIN billing_money_snapshots m ON m.id=b.snapshot_id JOIN billing_jobs j ON j.id=b.job_id WHERE b.job_id=?`, jobID).Scan(&raw, &site, &id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result billing.MoneySnapshot
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, err
	}
	if result.ID != id {
		return nil, fmt.Errorf("money snapshot binding mismatch")
	}
	if err = result.Validate(site); err != nil {
		return nil, err
	}
	return &result, nil
}
