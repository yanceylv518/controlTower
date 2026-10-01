package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"encoding/json"

	"fmt"
	"time"
)

func (s Store) CreateReportTask(ctx context.Context, t billing.ReportTask) (billing.ReportTask, error) {
	conn, e := s.db.Conn(ctx)
	if e != nil {
		return t, e
	}
	defer conn.Close()
	var locked int
	if e = conn.QueryRowContext(ctx, `SELECT GET_LOCK('ct:report:create',10)`).Scan(&locked); e != nil || locked != 1 {
		return t, billing.ErrReportBusy
	}
	defer conn.ExecContext(context.Background(), `SELECT RELEASE_LOCK('ct:report:create')`)
	tx, e := conn.BeginTx(ctx, nil)
	if e != nil {
		return t, e
	}
	defer tx.Rollback()
	var exists int
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM settlement_report_tasks WHERE id=?`, t.ID).Scan(&exists); e != nil {
		return t, e
	}
	if exists > 0 {
		return t, nil
	}
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM settlement_report_tasks WHERE instance_id=? AND status IN ('pending','running')`, t.Site).Scan(&exists); e != nil {
		return t, e
	}
	if exists > 0 {
		return t, billing.ErrReportBusy
	}
	from, e := time.ParseInLocation("2006-01-02", t.From, billing.BusinessLocation)
	if e != nil {
		return t, e
	}
	to, e := time.ParseInLocation("2006-01-02", t.To, billing.BusinessLocation)
	if e != nil {
		return t, e
	}
	if !from.Before(to) || to.Sub(from) > 32*24*time.Hour || to.After(billing.CompleteDayBoundary(time.Now())) {
		return t, fmt.Errorf("invalid report range")
	}
	t.Status = "pending"
	t.CreatedAt = time.Now().UTC()
	t.UpdatedAt = t.CreatedAt
	t.Days = []billing.ReportTaskDay{}
	_, e = tx.ExecContext(ctx, `INSERT INTO settlement_report_tasks(id,instance_id,range_from,range_to,status,overwrite_existing,automatic,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, t.ID, t.Site, t.From, t.To, t.Status, t.Overwrite, t.Automatic, t.CreatedAt, t.UpdatedAt)
	if e != nil {
		return t, e
	}
	for day := from; day.Before(to); day = day.AddDate(0, 0, 1) {
		d := billing.ReportTaskDay{Day: day.Format("2006-01-02"), Status: "pending", UpdatedAt: t.CreatedAt}
		if !t.Overwrite {
			if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM settlement_report_days WHERE instance_id=? AND bill_day=?`, t.Site, d.Day).Scan(&exists); e != nil {
				return t, e
			}
			if exists > 0 {
				d.Status = "reused"
			}
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO settlement_report_task_days(task_id,bill_day,status,updated_at) VALUES(?,?,?,?)`, t.ID, d.Day, d.Status, t.CreatedAt)
		if e != nil {
			return t, e
		}
		t.Days = append(t.Days, d)
	}
	if e = finishReportTask(ctx, tx, t.ID); e != nil {
		return t, e
	}
	if e = tx.QueryRowContext(ctx, `SELECT status FROM settlement_report_tasks WHERE id=?`, t.ID).Scan(&t.Status); e != nil {
		return t, e
	}
	e = tx.Commit()
	return t, e
}
func finishReportTask(ctx context.Context, tx *sql.Tx, id string) error {
	_, e := tx.ExecContext(ctx, `UPDATE settlement_report_tasks SET status=CASE WHEN status='cancelled' THEN status WHEN EXISTS(SELECT 1 FROM settlement_report_task_days WHERE task_id=? AND status IN ('pending','running')) THEN status WHEN EXISTS(SELECT 1 FROM settlement_report_task_days WHERE task_id=? AND status='failed') THEN 'failed' ELSE 'complete' END,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, id, id, id)
	return e
}
func (s Store) ListReportTasks(ctx context.Context, site string, limit int) ([]billing.ReportTask, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, e := s.db.QueryContext(ctx, `SELECT id,instance_id,DATE_FORMAT(range_from,'%Y-%m-%d'),DATE_FORMAT(range_to,'%Y-%m-%d'),status,overwrite_existing,automatic,created_at,updated_at FROM settlement_report_tasks WHERE instance_id=? ORDER BY created_at DESC LIMIT ?`, site, limit)
	if e != nil {
		return nil, e
	}
	out := []billing.ReportTask{}
	for rows.Next() {
		var t billing.ReportTask
		if e = rows.Scan(&t.ID, &t.Site, &t.From, &t.To, &t.Status, &t.Overwrite, &t.Automatic, &t.CreatedAt, &t.UpdatedAt); e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, t)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	waiting, e := s.GenerationWaitingFor(ctx, site, "report")
	if e != nil {
		return nil, e
	}
	for i := range out {
		if out[i].Status == "pending" {
			out[i].WaitingFor = waiting
		}
		r, e := s.db.QueryContext(ctx, `SELECT DATE_FORMAT(bill_day,'%Y-%m-%d'),status,processed,error_message,updated_at FROM settlement_report_task_days WHERE task_id=? ORDER BY bill_day`, out[i].ID)
		if e != nil {
			return nil, e
		}
		out[i].Days = []billing.ReportTaskDay{}
		for r.Next() {
			var d billing.ReportTaskDay
			if e = r.Scan(&d.Day, &d.Status, &d.Processed, &d.Error, &d.UpdatedAt); e != nil {
				r.Close()
				return nil, e
			}
			out[i].Days = append(out[i].Days, d)
		}
		e = r.Err()
		r.Close()
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (s Store) CancelReportTask(ctx context.Context, site, id string) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var status string
	if e = tx.QueryRowContext(ctx, `SELECT status FROM settlement_report_tasks WHERE id=? AND instance_id=? FOR UPDATE`, id, site).Scan(&status); e != nil {
		return e
	}
	if status != "pending" && status != "running" {
		return nil
	}
	if _, e = tx.ExecContext(ctx, `UPDATE settlement_report_tasks SET status='cancelled',updated_at=UTC_TIMESTAMP(6) WHERE id=?`, id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE settlement_report_task_days SET status='cancelled',updated_at=UTC_TIMESTAMP(6) WHERE task_id=? AND status IN ('pending','running')`, id); e != nil {
		return e
	}
	return tx.Commit()
}
func (s Store) ReadReportDays(ctx context.Context, site, from, to string) ([]billing.ReportDocument, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT payload FROM settlement_report_days WHERE instance_id=? AND bill_day>=? AND bill_day<? ORDER BY bill_day`, site, from, to)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []billing.ReportDocument{}
	for rows.Next() {
		var raw []byte
		var d billing.ReportDocument
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &d); e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s Store) ReportDayExists(ctx context.Context, site, day string) (bool, error) {
	var n int
	e := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM settlement_report_days WHERE instance_id=? AND bill_day=?`, site, day).Scan(&n)
	return n > 0, e
}
func (s Store) LockReportWorker(ctx context.Context) (context.Context, func(), error) {
	conn, e := s.db.Conn(ctx)
	if e != nil {
		return nil, nil, e
	}
	var ok int
	if e = conn.QueryRowContext(ctx, `SELECT GET_LOCK('ct:report:worker',0)`).Scan(&ok); e != nil || ok != 1 {
		conn.Close()
		return nil, nil, billing.ErrReportBusy
	}
	lease, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-lease.Done():
				return
			case <-tick.C:
				check, stop := context.WithTimeout(lease, 2*time.Second)
				var owns int
				e := conn.QueryRowContext(check, `SELECT COALESCE(IS_USED_LOCK('ct:report:worker')=CONNECTION_ID(),0)`).Scan(&owns)
				stop()
				if e != nil || owns != 1 {
					cancel()
					return
				}
			}
		}
	}()
	return lease, func() {
		cancel()
		<-done
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		conn.ExecContext(c, `SELECT RELEASE_LOCK('ct:report:worker')`)
		conn.Close()
	}, nil
}
func (s Store) RecoverReportTasks(ctx context.Context) error {
	_, e := s.db.ExecContext(ctx, `UPDATE settlement_report_task_days d JOIN settlement_report_tasks t ON t.id=d.task_id LEFT JOIN settlement_report_checkpoints c ON c.task_id=d.task_id AND c.bill_day=d.bill_day SET d.status='pending',d.processed=COALESCE(c.processed,0) WHERE d.status='running' AND t.status IN ('pending','running') AND (?='' OR t.instance_id=?)`, s.generationSite, s.generationSite)
	return e
}
func (s Store) NextReportTaskDay(ctx context.Context) (billing.ReportTask, billing.ReportTaskDay, error) {
	t := billing.ReportTask{}
	d := billing.ReportTaskDay{}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return t, d, e
	}
	defer tx.Rollback()
	e = tx.QueryRowContext(ctx, `SELECT t.id,t.instance_id,t.overwrite_existing,DATE_FORMAT(d.bill_day,'%Y-%m-%d'),d.processed FROM settlement_report_tasks t JOIN settlement_report_task_days d ON d.task_id=t.id WHERE t.status IN ('pending','running') AND d.status='pending' AND (?='' OR t.instance_id=?) AND (?='' OR t.id=?) ORDER BY t.automatic,t.created_at,d.bill_day LIMIT 1 FOR UPDATE`, s.generationSite, s.generationSite, s.generationJob, s.generationJob).Scan(&t.ID, &t.Site, &t.Overwrite, &d.Day, &d.Processed)
	if e != nil {
		return t, d, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE settlement_report_tasks SET status='running',updated_at=UTC_TIMESTAMP(6) WHERE id=?`, t.ID); e != nil {
		return t, d, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE settlement_report_task_days SET status='running',updated_at=UTC_TIMESTAMP(6) WHERE task_id=? AND bill_day=?`, t.ID, d.Day); e != nil {
		return t, d, e
	}
	return t, d, tx.Commit()
}
func (s Store) ReportTaskProgress(ctx context.Context, id, day string, n int64) error {
	result, e := s.db.ExecContext(ctx, `UPDATE settlement_report_task_days d JOIN settlement_report_tasks t ON t.id=d.task_id SET d.processed=GREATEST(d.processed,?),d.updated_at=UTC_TIMESTAMP(6) WHERE d.task_id=? AND d.bill_day=? AND d.status='running' AND t.status='running'`, n, id, day)
	if e != nil {
		return e
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		var status string
		e = s.db.QueryRowContext(ctx, `SELECT status FROM settlement_report_tasks WHERE id=?`, id).Scan(&status)
		if e != nil {
			return e
		}
		if status != "running" {
			return billing.ErrReportCancelled
		}
	}
	return nil
}
func (s Store) FinishReportTaskDay(ctx context.Context, t billing.ReportTask, d billing.ReportTaskDay, doc *billing.ReportDocument, message string) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var status string
	if e = tx.QueryRowContext(ctx, `SELECT status FROM settlement_report_tasks WHERE id=? FOR UPDATE`, t.ID).Scan(&status); e != nil {
		return e
	}
	if status != "running" {
		return billing.ErrReportCancelled
	}
	status = "failed"
	if message == "reused" {
		status = "reused"
		message = ""
	}
	if doc != nil {
		payload, e := json.Marshal(doc)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO settlement_report_days(instance_id,bill_day,payload,task_id,updated_at) VALUES(?,?,?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE payload=VALUES(payload),task_id=VALUES(task_id),updated_at=VALUES(updated_at)`, t.Site, d.Day, payload, t.ID); e != nil {
			return e
		}
		status = "complete"
	}
	if doc != nil || status == "reused" {
		if _, e = tx.ExecContext(ctx, `DELETE FROM settlement_report_checkpoints WHERE task_id=? AND bill_day=?`, t.ID, d.Day); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE settlement_report_task_days SET status=?,error_message=?,updated_at=UTC_TIMESTAMP(6) WHERE task_id=? AND bill_day=?`, status, message, t.ID, d.Day); e != nil {
		return e
	}
	if e = finishReportTask(ctx, tx, t.ID); e != nil {
		return e
	}
	return tx.Commit()
}

var _ billing.ReportStore = Store{}

var _ = sql.ErrNoRows
