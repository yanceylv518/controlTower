package logarchive

import (
	"context"
	af "controltower/internal/archivecontract"
	"database/sql"
	"encoding/json"
	"time"
)

func (w *Worker) WorkflowDayPage(ctx context.Context, after string) (*af.WorkflowDayPage, error) {
	if after != "" {
		if _, _, err := af.DateBounds(after); err != nil {
			return nil, err
		}
	}
	if after == "" {
		after = "1969-12-31"
	}
	if err := w.refreshRawInventory(ctx); err != nil {
		return nil, err
	}
	tx, err := w.target.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	s, err := loadWorkflow(ctx, tx)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p := &af.WorkflowDayPage{TaskID: s.TaskID}
	var observed string
	if err = tx.QueryRowContext(ctx, `SELECT DATE_FORMAT(UTC_TIMESTAMP(6),'%Y-%m-%d %H:%i:%s.%f')`).Scan(&observed); err != nil {
		return nil, err
	}
	p.ObservedAt, err = time.Parse("2006-01-02 15:04:05.999999", observed)
	if err != nil {
		return nil, err
	}
	// Each arm uses the existing date primary key; only daily summaries are read.
	rows, err := tx.QueryContext(ctx, `SELECT dates.day,stats.log_rows,stats.request_rows,stats.error_rows,
	COALESCE(w.state,''),COALESCE(w.error_code,''),DATE_FORMAT(w.updated_at,'%Y-%m-%d %H:%i:%s.%f'),
	COALESCE(w.revision=d.mutation_revision,0)
	FROM (
	 (SELECT period_key AS day FROM log_daily_stats WHERE period_key>? AND period_key BETWEEN '1970-01-01' AND '9998-12-31' ORDER BY period_key LIMIT ?)
	 UNION (SELECT DATE_FORMAT(log_date,'%Y-%m-%d') FROM archive_days WHERE log_date>? ORDER BY log_date LIMIT ?)
	 UNION (SELECT DATE_FORMAT(log_date,'%Y-%m-%d') FROM archive_workflow_days WHERE log_date>? ORDER BY log_date LIMIT ?)
	) dates
	LEFT JOIN log_daily_stats stats ON stats.period_key=dates.day
	LEFT JOIN archive_days d ON d.log_date=dates.day
	LEFT JOIN archive_workflow_days w ON w.log_date=dates.day
	ORDER BY dates.day LIMIT ?`, after, af.WorkflowDayPageSize+1, after, af.WorkflowDayPageSize+1, after, af.WorkflowDayPageSize+1, af.WorkflowDayPageSize+1)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d af.WorkflowDay
		var logs, requests, errors sql.NullString
		var updated sql.NullString
		var revisionMatches bool
		if err = rows.Scan(&d.Date, &logs, &requests, &errors, &d.State, &d.ErrorCode, &updated, &revisionMatches); err != nil {
			rows.Close()
			return nil, err
		}
		if len(p.Days) == af.WorkflowDayPageSize {
			p.NextAfter = p.Days[len(p.Days)-1].Date
			break
		}
		d.ObservedAt = p.ObservedAt
		if updated.Valid {
			parsed, e := time.Parse("2006-01-02 15:04:05.999999", updated.String)
			if e != nil {
				rows.Close()
				return nil, e
			}
			d.UpdatedAt = &parsed
		}
		if logs.Valid && requests.Valid && errors.Valid {
			d.Counts = &af.WorkflowDayCounts{LogRows: logs.String, RequestRows: requests.String, ErrorRows: errors.String}
		}
		if !revisionMatches || (d.State != "sealed" && d.State != "blocked") {
			d.State, d.ErrorCode, d.UpdatedAt = "pending", "", nil
		}
		switch s.Phase {
		case "reset_state", "reset_daily", "reset_monthly":
			d.State, d.Counts, d.ErrorCode, d.UpdatedAt = "preparing", nil, "", nil
		case "import_target":
			d.State, d.ErrorCode, d.UpdatedAt = "pending", "", nil
			if d.Counts != nil {
				d.State = "rebuilding"
			}
		case "backfill", "verify", "seal":
			if d.Date == s.Date {
				d.State, d.ErrorCode, d.UpdatedAt = s.Phase, s.ErrorCode, nil
			}
		}
		p.Days = append(p.Days, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// A selected empty date may not have a daily stats/catalog row yet. It is
	// still shown by the UI from WorkflowStatus.Date, without invented counts.
	w.mergeRawInventory(p, after)
	var rawPipeline []byte
	pipelineErr := tx.QueryRowContext(ctx, `SELECT state_json FROM archive_pipeline WHERE singleton_id=1`).Scan(&rawPipeline)
	if pipelineErr != nil && pipelineErr != sql.ErrNoRows {
		return nil, pipelineErr
	}
	if pipelineErr == nil {
		var pipeline pipelineCheckpoint
		if json.Unmarshal(rawPipeline, &pipeline) != nil || pipeline.State.Validate() != nil {
			return nil, ErrWriterCheckpoint
		}
		for i := range p.Days {
			d := &p.Days[i]
			d.State = pipeline.State.DayState(d.Date)
			d.ErrorCode = ""
			d.UpdatedAt = nil
			if !pipeline.State.MigrationDone {
				d.Counts = nil
			}
			if day := pipeline.State.Days[d.Date]; day != nil && day.Result != nil {
				d.ErrorCode = day.Result.Code
				d.UpdatedAt = day.Result.CompletedAt
				d.Diagnostic = day.Result.Diagnostic
			}
			if d.State == "verification" && s.Date == d.Date && (s.Phase == "backfill" || s.Phase == "verify" || s.Phase == "seal") {
				d.State = s.Phase
			}
		}
	}
	if err = p.Validate(); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return p, nil
}
