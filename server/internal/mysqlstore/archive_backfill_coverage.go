package mysqlstore

import (
	"context"
	"time"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
)

func (s Store) GetArchiveCoverage(ctx context.Context, site, dataset, month string) (ac.ArchiveCoverageMonth, error) {
	out := ac.ArchiveCoverageMonth{Month: month, Days: []ac.ArchiveCoverageDay{}}
	start, err := time.ParseInLocation("2006-01", month, archiveBeijing)
	if err != nil || start.Year() < 1970 || start.Year() > 9998 || start.Format("2006-01") != month {
		return out, af.ErrConflict
	}
	d, err := s.GetArchiveDataset(ctx, site, dataset)
	if err != nil {
		return out, err
	}
	out.Identity = d.Identity
	p, err := archivePolicy(ctx, s.db, dataset)
	if err != nil {
		return out, err
	}
	out.Policy = p
	var now time.Time
	if err = s.db.QueryRowContext(ctx, `SELECT UTC_TIMESTAMP(6)`).Scan(&now); err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, archiveTaskSelect+`WHERE dataset_id=? AND log_date>=? AND log_date<? AND task_type IN ('date_backfill','recent_backfill') ORDER BY log_date,created_at DESC,task_id DESC`, archiveIDBytes(dataset), start.Format("2006-01-02"), start.AddDate(0, 1, 0).Format("2006-01-02"))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	latest := map[string]ac.BackfillTaskItem{}
	for rows.Next() {
		item, e := archiveScanTask(rows)
		if e != nil {
			return out, e
		}
		if _, ok := latest[item.Date]; !ok {
			latest[item.Date] = item
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	today := now.In(archiveBeijing).Format("2006-01-02")
	for date := start; date.Before(start.AddDate(0, 1, 0)); date = date.AddDate(0, 0, 1) {
		day := ac.ArchiveCoverageDay{Date: date.Format("2006-01-02"), State: "needs_fill"}
		if task, ok := latest[day.Date]; ok {
			day.Task = &task
		}
		switch {
		case day.Date > today:
			day.State = "future"
		case day.Date == today:
			day.State = "today"
		case p.CoverageFrom == "" || day.Date < p.CoverageFrom:
			day.State = "unknown_history"
		case p.SourceRetainedFrom != "" && day.Date < p.SourceRetainedFrom:
			day.State, day.BlockReason = "blocked", "source_cleared"
		case day.Task != nil && day.Task.State == "blocked":
			day.State, day.BlockReason = "blocked", day.Task.ErrorCode
		case day.Task != nil && day.Task.State == "succeeded" && day.Task.Progress != nil:
			day.State = "scanned_pending_verify"
			if day.Task.Progress.EmptyCandidate {
				if p.SourceRetainedFrom == "" {
					day.State, day.BlockReason = "unknown_history", "source_history_unknown"
				} else {
					day.State = "empty_candidate"
				}
			}
		}
		if d.UnscopedBlockingIssues > 0 && day.Date < today && day.State != "unknown_history" {
			day.State, day.BlockReason = "blocked", "unscoped_blocking_issues"
		}
		out.Days = append(out.Days, day)
	}
	return out, nil
}
