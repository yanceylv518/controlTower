package mysqlstore

import (
	"context"
	"fmt"
)

func (s Store) BindBillingArchiveVersion(ctx context.Context, job, day, source, version string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO billing_archive_versions(job_id,bill_day,source_hash,version_id) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE job_id=VALUES(job_id)`, job, day, source, version); err != nil {
		return err
	}
	var oldSource, oldVersion string
	if err = tx.QueryRowContext(ctx, `SELECT source_hash,version_id FROM billing_archive_versions WHERE job_id=? AND bill_day=?`, job, day).Scan(&oldSource, &oldVersion); err != nil {
		return err
	}
	if oldSource != source || oldVersion != version {
		return fmt.Errorf("archive day version changed during billing")
	}
	return tx.Commit()
}
