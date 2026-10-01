package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"controltower/server/internal/billing"
)

func (s Store) LoadReportCheckpoint(ctx context.Context, task, day string) (*billing.ReportCheckpoint, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM settlement_report_checkpoints WHERE task_id=? AND bill_day=?`, task, day).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cp billing.ReportCheckpoint
	if err = json.Unmarshal(payload, &cp); err != nil {
		return nil, fmt.Errorf("report checkpoint decode: %w", err)
	}
	return &cp, nil
}

func (s Store) SaveReportCheckpoint(ctx context.Context, task, day string, cp *billing.ReportCheckpoint) error {
	payload, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Same task-first lock order as cancel/publish; a cancelled task cannot save.
	var status, site string
	if err = tx.QueryRowContext(ctx, `SELECT status,instance_id FROM settlement_report_tasks WHERE id=? FOR UPDATE`, task).Scan(&status, &site); err != nil {
		return err
	}
	if status != "running" {
		return billing.ErrReportCancelled
	}
	if cp.Site != site || cp.From.In(billing.BusinessLocation).Format("2006-01-02") != day {
		return fmt.Errorf("report checkpoint scope mismatch")
	}
	if err = tx.QueryRowContext(ctx, `SELECT status FROM settlement_report_task_days WHERE task_id=? AND bill_day=? FOR UPDATE`, task, day).Scan(&status); err != nil {
		return err
	}
	if status != "running" {
		return billing.ErrReportCancelled
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO settlement_report_checkpoints(task_id,bill_day,processed,payload,updated_at) VALUES(?,?,?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE processed=VALUES(processed),payload=VALUES(payload),updated_at=VALUES(updated_at)`, task, day, cp.Processed, payload); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE settlement_report_task_days SET processed=?,updated_at=UTC_TIMESTAMP(6) WHERE task_id=? AND bill_day=?`, cp.Processed, task, day); err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) RetryReportTask(ctx context.Context, site, id string) error {
	// Share create's named lock so retry and a newly submitted task cannot both
	// become runnable for the same site. Completed days stay untouched.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked int
	if err = conn.QueryRowContext(ctx, `SELECT GET_LOCK('ct:report:create',10)`).Scan(&locked); err != nil || locked != 1 {
		return billing.ErrReportBusy
	}
	defer conn.ExecContext(context.Background(), `SELECT RELEASE_LOCK('ct:report:create')`)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM settlement_report_tasks WHERE id=? AND instance_id=? FOR UPDATE`, id, site).Scan(&status); err != nil {
		return err
	}
	if status != "failed" && status != "cancelled" {
		return billing.ErrReportBusy
	}
	var busy int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM settlement_report_tasks WHERE instance_id=? AND status IN ('pending','running')`, site).Scan(&busy); err != nil {
		return err
	}
	if busy > 0 {
		return billing.ErrReportBusy
	}
	// A newer task may have published this date after the failure. Resuming the
	// older snapshot must not silently replace that newer report.
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM settlement_report_task_days d JOIN settlement_report_days r ON r.instance_id=? AND r.bill_day=d.bill_day JOIN settlement_report_tasks t ON t.id=d.task_id WHERE d.task_id=? AND d.status IN ('failed','cancelled') AND r.task_id<>t.id AND r.updated_at>t.created_at`, site, id).Scan(&busy); err != nil {
		return err
	}
	if busy > 0 {
		return fmt.Errorf("report_retry_obsolete")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE settlement_report_task_days d LEFT JOIN settlement_report_checkpoints c ON c.task_id=d.task_id AND c.bill_day=d.bill_day SET d.status='pending',d.processed=COALESCE(c.processed,0),d.error_message='',d.updated_at=UTC_TIMESTAMP(6) WHERE d.task_id=? AND d.status IN ('failed','cancelled')`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE settlement_report_tasks SET status='pending',updated_at=UTC_TIMESTAMP(6) WHERE id=?`, id); err != nil {
		return err
	}
	if err = finishReportTask(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

var _ billing.ReportCheckpointStore = Store{}
