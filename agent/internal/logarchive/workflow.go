package logarchive

import (
	"context"
	af "controltower/internal/archivecontract"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type workflowState struct {
	Origin   string            `json:"origin,omitempty"`
	Group    []string          `json:"group,omitempty"`
	Verified map[string]uint64 `json:"verified,omitempty"`
	Turns    uint64            `json:"turns,string"`
	TaskID   string            `json:"task_id"`
	af.WorkflowStatus
	Table         string            `json:"table,omitempty"`
	AfterID       int64             `json:"after_id,string"`
	NextDate      string            `json:"next_date,omitempty"`
	CurrentScan   *af.BackfillTask  `json:"current_scan,omitempty"`
	Scan          *af.BackfillTask  `json:"scan,omitempty"`
	Verify        *af.ReconcileTask `json:"verify,omitempty"`
	Seal          *af.SealTask      `json:"seal,omitempty"`
	ConfigVersion int64             `json:"config_version"`
}

func (w *Worker) AcquireWorkflow(ctx context.Context, g af.WriterGrant, remaining time.Duration) error {
	return w.acquireWriter(ctx, g, remaining, true)
}

func loadWorkflow(ctx context.Context, q foundationQuery) (workflowState, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT state_json FROM archive_workflow WHERE singleton_id=1`).Scan(&raw)
	var state workflowState
	if err != nil {
		return state, err
	}
	if json.Unmarshal(raw, &state) != nil || state.Validate() != nil {
		return state, ErrWriterCheckpoint
	}
	return state, nil
}
func saveWorkflow(ctx context.Context, tx *sql.Tx, s workflowState) error {
	if s.Validate() != nil {
		return ErrWriterCheckpoint
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO archive_workflow(singleton_id,state_json,updated_at) VALUES(1,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE state_json=VALUES(state_json),updated_at=VALUES(updated_at)`, string(raw))
	return err
}
func initializeWorkflow(ctx context.Context, tx *sql.Tx) error {
	_, err := loadWorkflow(ctx, tx)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var checkpoints int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM archive_checkpoints WHERE stream_key='incremental'`).Scan(&checkpoints); err != nil {
		return err
	}
	state := workflowState{WorkflowStatus: af.WorkflowStatus{Phase: "live"}}
	state.TaskID, err = newArchiveID()
	if err != nil {
		return err
	}
	if checkpoints == 0 {
		var revision uint64
		if err = tx.QueryRowContext(ctx, `SELECT catalog_revision FROM archive_dataset_meta WHERE singleton_id=1`).Scan(&revision); err != nil {
			return err
		}
		if revision != 0 {
			return ErrWriterCheckpoint
		}
		// Only unversioned legacy derived data may be rebuilt. Never reset a
		// damaged modern archive or infer progress from a maximum source ID.
		for _, table := range []string{"archive_checkpoints", "archive_batch_receipts", "archive_days", "archive_day_versions", "archive_scan_tasks", "archive_reconcile_runs", "archive_seal_builds", "logs"} {
			var count int
			if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM (SELECT 1 FROM "+quote(table)+" LIMIT 1) present").Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return ErrWriterCheckpoint
			}
		}
		state.Phase = "reset_state"
	} else if err = requireWriterBaseline(ctx, tx); err != nil {
		return err
	}
	return saveWorkflow(ctx, tx, state)
}

func (w *Worker) WorkflowProgress(ctx context.Context) (*af.WorkflowStatus, error) {
	reportOperation(ctx, "read_progress", "archive", "archive_workflow")
	s, err := loadWorkflow(ctx, w.target)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	reportOperation(ctx, "read_progress", "archive", "archive_workflow_days")
	if err = w.target.QueryRowContext(ctx, `SELECT COALESCE(SUM(w.state='sealed' AND w.revision=d.mutation_revision),0),COALESCE(SUM(w.state='blocked'),0) FROM archive_workflow_days w LEFT JOIN archive_days d ON d.log_date=w.log_date`).Scan(&s.CompletedDays, &s.BlockedDays); err != nil {
		return nil, err
	}
	rows, err := w.target.QueryContext(ctx, `SELECT DATE_FORMAT(log_date,'%Y-%m-%d'),error_code FROM archive_workflow_days WHERE state='blocked' ORDER BY log_date LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var issue af.WorkflowIssue
		if err = rows.Scan(&issue.Date, &issue.Code); err != nil {
			return nil, err
		}
		s.Issues = append(s.Issues, issue)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return &s.WorkflowStatus, nil
}

func (w *Worker) commitWorkflow(ctx context.Context, g af.WriterGrant, s workflowState) error {
	reportOperation(ctx, "save_workflow", "archive", "archive_workflow")
	tx, err := w.target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	meta, err := readWriterMeta(ctx, tx, g.Identity, true)
	if err != nil {
		return err
	}
	if err = requireWriter(meta, g); err != nil {
		return err
	}
	if err = saveWorkflow(ctx, tx, s); err != nil {
		return err
	}
	if err = writerGuardBeforeCommit(ctx, tx, g); err != nil {
		return err
	}
	return tx.Commit()
}

// WorkflowPass performs one bounded unit of the single site-wide operation.
// A caller must serialize it with other operations and renew the writer lease.
func (w *Worker) WorkflowPass(ctx context.Context, g af.WriterGrant, remaining time.Duration, immutable bool) (*af.WorkflowStatus, error) {
	ctx = workflowOperationContext(ctx, "acquire", "")
	if err := w.acquireWriter(ctx, g, remaining, true); err != nil {
		return nil, err
	}
	reportOperation(ctx, "advance_workflow", "archive", "archive_workflow")
	s, err := loadWorkflow(ctx, w.target)
	if err != nil {
		return nil, err
	}
	ctx = workflowOperationContext(ctx, s.Phase, s.Date)
	if s.ConfigVersion != g.ConfigVersion {
		// Policy changes may retry blocked dates, but never replace a frozen build.
		s.ConfigVersion = g.ConfigVersion
		if err = w.commitWorkflow(ctx, g, s); err != nil {
			return nil, err
		}
	}
	s.ErrorCode = ""
	reportOperation(ctx, "advance_workflow", "archive", "archive_workflow")
	switch s.Phase {
	case "reset_state", "reset_daily", "reset_monthly", "import_target":
		err = w.importWorkflowPage(ctx, g, &s)
	case "source_scan":
		// Resume old persisted workflows without reading the ID stream again.
		s.Phase = "live"
		err = w.commitWorkflow(ctx, g, s)
	case "live":
		err = w.selectWorkflowDay(ctx, g, &s, immutable)
	case "backfill":
		reportOperation(ctx, "prepare_date_scan", "archive", "archive_scan_tasks")
		if s.Scan == nil {
			return nil, ErrWriterCheckpoint
		}
		restart, e := w.copyScanNeedsRestart(ctx, *s.Scan)
		if e != nil {
			return nil, e
		}
		if restart {
			id, e := newArchiveID()
			if e != nil {
				return nil, e
			}
			wasCurrent := s.CurrentScan != nil && s.CurrentScan.TaskID == s.Scan.TaskID
			s.Scan.TaskID = id
			if wasCurrent {
				s.CurrentScan = s.Scan
			}
			return w.WorkflowProgressAfterCommit(ctx, g, s)
		}
		var result af.BackfillStatus
		result, err = w.scanDate(ctx, g, *s.Scan, true)
		if err == nil && (result.State == "succeeded" || result.State == "blocked") && s.CurrentScan != nil && s.Scan.TaskID == s.CurrentScan.TaskID {
			// An open-day cursor can miss inserts behind it. Once the day closes,
			// take one fresh bounded closing pass to capture final day evidence.
			id, e := newArchiveID()
			if e != nil {
				return nil, e
			}
			copy := *s.Scan
			copy.TaskID = id
			s.Scan = &copy
			s.CurrentScan = nil
			err = w.commitWorkflow(ctx, g, s)
		} else if err == nil && result.State == "succeeded" {
			if !immutable {
				err = w.finishWorkflowDay(ctx, g, &s, "source_history_unconfirmed")
			} else {
				id, e := newArchiveID()
				if e != nil {
					return nil, e
				}
				s.Verify = &af.ReconcileTask{Identity: g.Identity, TaskID: id, Date: s.Date, Attempt: 1, Policy: s.Scan.Policy, Assurance: af.VerificationAssurance{StableBeforeUnix: result.SourceNowUnix - int64(w.delay/time.Second), ValidUntilUnix: time.Now().Add(24 * time.Hour).Unix(), Evidence: "operator_full_history_retained_and_immutable"}}
				s.Phase = "verify"
				err = w.commitWorkflow(ctx, g, s)
			}
		} else if result.State == "blocked" {
			err = w.finishWorkflowDay(ctx, g, &s, result.ErrorCode)
		} else if err == nil && s.CurrentScan != nil && s.Scan.TaskID == s.CurrentScan.TaskID {
			// Yield between current-day pages so pending historical work can run.
			s.Phase = "live"
			err = w.commitWorkflow(ctx, g, s)
		}
	case "verify":
		reportOperation(ctx, "verify_archive", "archive", "")
		if s.Verify == nil {
			return nil, ErrWriterCheckpoint
		}
		if s.Scan == nil {
			return nil, ErrWriterCheckpoint
		}
		restart, e := w.copyScanNeedsRestart(ctx, *s.Scan)
		if e != nil {
			return nil, e
		}
		if restart {
			id, e := newArchiveID()
			if e != nil {
				return nil, e
			}
			s.Scan.TaskID = id
			s.Verify = nil
			s.Phase = "backfill"
			return w.WorkflowProgressAfterCommit(ctx, g, s)
		}
		var result af.ReconcileStatus
		result, err = w.reconcileCopiedDate(ctx, g, *s.Verify, *s.Scan)
		if err == nil && result.State == "matched" {
			if s.Verified == nil {
				s.Verified = map[string]uint64{}
			}
			s.Verified[s.Date] = result.FinalRevision
			err = w.advanceWorkflowGroup(ctx, g, &s)
		} else if result.State == "blocked" || result.State == "mismatched" {
			code := result.ErrorCode
			if code == "" {
				code = "verification_mismatched"
			}
			err = w.finishWorkflowDay(ctx, g, &s, code)
		}
	case "seal":
		reportOperation(ctx, "build_sealed_version", "archive", "")
		if s.Seal == nil {
			return nil, ErrWriterCheckpoint
		}
		var result af.SealStatus
		result, err = w.SealDaysV4(ctx, g, *s.Seal)
		if err == nil && result.State == "succeeded" {
			err = w.finishWorkflowDay(ctx, g, &s, "")
		} else if result.State == "blocked" {
			err = w.finishWorkflowDay(ctx, g, &s, result.ErrorCode)
		}
	default:
		return nil, ErrWriterCheckpoint
	}
	progressCtx := ctx
	if err != nil {
		progressCtx = WithOperationObserver(ctx, nil)
	}
	status, readErr := w.WorkflowProgress(progressCtx)
	if err != nil {
		if status != nil {
			status.ErrorCode = scanFailureCode(err)
		}
		return status, err
	}
	return status, readErr
}

func (w *Worker) selectWorkflowDay(ctx context.Context, g af.WriterGrant, s *workflowState, immutable bool) error {
	reportOperation(ctx, "check_source_index", "source", "logs")
	if err := w.requireScanIndex(ctx); err != nil {
		return err
	}
	// A chronological cursor cannot silently skip rows outside the date domain.
	var invalidID int64
	reportOperation(ctx, "select_source_date", "source", "logs")
	invalidErr := w.source.QueryRowContext(ctx, `SELECT id FROM logs WHERE created_at IS NULL OR created_at<=0 ORDER BY created_at,id LIMIT 1`).Scan(&invalidID)
	if invalidErr == nil {
		return &scanError{code: "source_invalid_date"}
	}
	if !errors.Is(invalidErr, sql.ErrNoRows) {
		return invalidErr
	}
	if s.FirstDate == "" {
		reportOperation(ctx, "select_archive_date", "archive", "archive_days")
		var first sql.NullString
		if err := w.target.QueryRowContext(ctx, `SELECT DATE_FORMAT(MIN(log_date),'%Y-%m-%d') FROM archive_days`).Scan(&first); err != nil {
			return err
		}
		var sourceFirst int64
		reportOperation(ctx, "select_source_date", "source", "logs")
		sourceErr := w.source.QueryRowContext(ctx, `SELECT created_at FROM logs WHERE created_at>0 ORDER BY created_at,id LIMIT 1`).Scan(&sourceFirst)
		if sourceErr != nil && !errors.Is(sourceErr, sql.ErrNoRows) {
			return sourceErr
		}
		if sourceErr == nil {
			date := time.Unix(sourceFirst, 0).In(archiveLocation).Format("2006-01-02")
			if !first.Valid || date < first.String {
				first = sql.NullString{String: date, Valid: true}
			}
		}
		if !first.Valid {
			return nil
		}
		s.FirstDate = first.String
	}
	if s.NextDate == "" {
		s.NextDate = s.FirstDate
	}
	cutoff := time.Now().Add(-w.delay).In(archiveLocation).Format("2006-01-02")
	if s.FirstDate > cutoff {
		// A future timestamp must not initialize a task before its coverage start.
		s.FirstDate = ""
		s.NextDate = ""
		return w.commitWorkflow(ctx, g, *s)
	}
	date := s.NextDate
	reportOperation(ctx, "select_archive_date", "archive", "archive_days")
	if date >= cutoff {
		err := w.target.QueryRowContext(ctx, `SELECT DATE_FORMAT(d.log_date,'%Y-%m-%d') FROM archive_days d LEFT JOIN archive_workflow_days w ON w.log_date=d.log_date WHERE d.log_date<? AND (w.log_date IS NULL OR w.revision<>d.mutation_revision OR (w.state='blocked' AND (w.config_version<>? OR (w.error_code='cohort_not_ended' AND w.updated_at<UTC_TIMESTAMP()-INTERVAL 6 HOUR)))) ORDER BY d.log_date LIMIT 1`, cutoff, g.ConfigVersion).Scan(&date)
		if errors.Is(err, sql.ErrNoRows) {
			date = cutoff
		} else if err != nil {
			return err
		}
	}
	id, err := newArchiveID()
	if err != nil {
		return err
	}
	p := af.DefaultCoveragePolicy()
	p.Budget = w.readBudget()
	p.CoverageFrom = s.FirstDate
	p.Evidence = "discovered_archive_date_range_not_proof_of_retention"
	if immutable {
		p.SourceRetainedFrom = s.FirstDate
		p.Evidence = "operator_full_history_retained_and_immutable"
	}
	s.Date = date
	s.Origin = date
	s.Group = []string{date}
	s.Verified = map[string]uint64{}
	s.Scan = &af.BackfillTask{Identity: g.Identity, TaskID: id, Date: date, Type: "date_backfill", Attempt: 1, Policy: p}
	if s.CurrentScan != nil && s.CurrentScan.Date == date && s.CurrentScan.Policy.SourceRetainedFrom == p.SourceRetainedFrom {
		s.Scan = s.CurrentScan
	} else if date >= cutoff {
		s.CurrentScan = s.Scan
	}
	s.Phase = "backfill"
	return w.commitWorkflow(ctx, g, *s)
}
func (w *Worker) finishWorkflowDay(ctx context.Context, g af.WriterGrant, s *workflowState, code string) error {
	tx, err := w.target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	meta, err := readWriterMeta(ctx, tx, g.Identity, true)
	if err != nil {
		return err
	}
	if err = requireWriter(meta, g); err != nil {
		return err
	}
	state := "sealed"
	if code != "" {
		state = "blocked"
	}
	for _, date := range s.Group {
		_, err = tx.ExecContext(ctx, `INSERT INTO archive_workflow_days(log_date,state,revision,config_version,error_code,updated_at) SELECT ?,?,COALESCE((SELECT mutation_revision FROM archive_days WHERE log_date=?),0),?,?,UTC_TIMESTAMP(6) ON DUPLICATE KEY UPDATE state=VALUES(state),revision=VALUES(revision),config_version=VALUES(config_version),error_code=VALUES(error_code),updated_at=VALUES(updated_at)`, date, state, date, g.ConfigVersion, code)
		if err != nil {
			return err
		}
	}
	if s.Origin >= s.NextDate {
		day, _ := time.Parse("2006-01-02", s.Origin)
		s.NextDate = day.AddDate(0, 0, 1).Format("2006-01-02")
	}
	if s.CurrentScan != nil && s.CurrentScan.Date == s.Origin {
		s.CurrentScan = nil
	}
	s.Phase = "live"
	s.ErrorCode = code
	s.Scan = nil
	s.Verify = nil
	s.Seal = nil
	if err = saveWorkflow(ctx, tx, *s); err != nil {
		return err
	}
	if err = writerGuardBeforeCommit(ctx, tx, g); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *Worker) WorkflowProgressAfterCommit(ctx context.Context, g af.WriterGrant, s workflowState) (*af.WorkflowStatus, error) {
	if err := w.commitWorkflow(ctx, g, s); err != nil {
		return nil, err
	}
	return w.WorkflowProgress(ctx)
}
