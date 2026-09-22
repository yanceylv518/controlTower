package mysqlstore

import (
	"context"
	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
	"database/sql"
	"encoding/json"
	"time"
)

func saveWorkflowDayPage(ctx context.Context, tx *sql.Tx, st ac.Status) error {
	p := st.WorkflowDaily
	if p == nil {
		return nil
	}
	if err := p.Validate(); err != nil {
		return err
	}
	for _, day := range p.Days {
		raw, err := json.Marshal(day)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO archive_workflow_day_reports(dataset_id,workflow_id,log_date,report_json,observed_at) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE report_json=IF(observed_at<=VALUES(observed_at),VALUES(report_json),report_json),observed_at=GREATEST(observed_at,VALUES(observed_at))`, archiveIDBytes(st.Foundation.DatasetID), archiveIDBytes(p.TaskID), day.Date, string(raw), p.ObservedAt.UTC())
		if err != nil {
			return err
		}
	}
	return nil
}

func (s Store) ListArchiveWorkflowDays(ctx context.Context, site, month string) ([]af.WorkflowDay, error) {
	start, err := time.Parse("2006-01", month)
	if err != nil {
		return nil, err
	}
	// Both the active dataset and current workflow identity come from the
	// authenticated executor's accepted report; legacy site receipts never mix.
	rows, err := s.db.QueryContext(ctx, `SELECT r.report_json FROM site_log_archive_control c JOIN archive_workflow_day_reports r ON r.dataset_id=c.active_dataset_id AND r.workflow_id=UNHEX(JSON_UNQUOTE(JSON_EXTRACT(c.status_json,'$.workflow_daily.task_id'))) WHERE c.site_id=? AND r.log_date>=? AND r.log_date<? ORDER BY r.log_date`, site, start.Format("2006-01-02"), start.AddDate(0, 1, 0).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := []af.WorkflowDay{}
	for rows.Next() {
		var raw []byte
		var d af.WorkflowDay
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &d); err != nil {
			return nil, err
		}
		days = append(days, d)
	}
	return days, rows.Err()
}
