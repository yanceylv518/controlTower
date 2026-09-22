package logarchive

import (
	"context"
	af "controltower/internal/archivecontract"
	ap "controltower/internal/archivepipeline"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
)

func (w *Worker) PipelinePass(ctx context.Context, g af.WriterGrant, remaining time.Duration, settings ap.Settings, immutable bool) (*ap.Status, error) {
	if err := w.AcquireWorkflow(ctx, g, remaining); err != nil {
		return nil, err
	}
	if err := w.initializePipeline(ctx, g, settings); err != nil {
		return nil, err
	}
	// Cancelling verification must release its date freeze before collection
	// can acquire the source slot. Published immutable versions remain intact.
	if !settings.Verification {
		s, err := loadWorkflow(ctx, w.target)
		if err != nil {
			return nil, err
		}
		if s.Phase == "seal" && s.Seal != nil {
			if _, err = w.abortSealBuild(ctx, g, *s.Seal, "pipeline_paused"); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
		}
		if err = w.pipelineTransaction(ctx, g, func(tx *sql.Tx, p *pipelineCheckpoint) error {
			if _, ok := p.State.Active[ap.Verification]; !ok {
				return nil
			}
			delete(p.State.Active, ap.Verification)
			p.VerificationToken = 0
			s.Phase = "live"
			s.Scan = nil
			s.Verify = nil
			s.Seal = nil
			return saveWorkflow(ctx, tx, s)
		}); err != nil {
			return nil, err
		}
	}
	var work []ap.Work
	err := w.pipelineTransaction(ctx, g, func(tx *sql.Tx, p *pipelineCheckpoint) error {
		if p.State.MigrationDone {
			if _, ok := p.State.Active[ap.Verification]; !ok {
				if v, e := p.State.Reserve(ap.Verification); e == nil {
					_ = v
				}
				if _, ok = p.State.Active[ap.Verification]; !ok {
					if o, e := p.State.Reserve(ap.Organization); e == nil {
						p.AfterID = 0
						p.AfterCreated = 0
						_ = o
					}
				}
			}
		} else {
			_, _ = p.State.Reserve(ap.Migration)
		}
		if !p.CollectionDone && pipelineRetryReady(p, ap.Collection) {
			_, _ = p.State.Reserve(ap.Collection)
		}
		for _, task := range []ap.Task{ap.Migration, ap.Organization, ap.Verification, ap.Collection} {
			active, ok := p.State.Active[task]
			if !ok {
				continue
			}
			enabled := map[ap.Task]bool{ap.Migration: settings.Migration, ap.Organization: settings.Organization, ap.Verification: settings.Verification, ap.Collection: settings.Collection}[task]
			if enabled && pipelineRetryReady(p, task) {
				work = append(work, active)
			} else if task == ap.Collection {
				delete(p.State.Active, task)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var wg sync.WaitGroup
	var resultMu sync.Mutex
	var failures []error
	for _, job := range work {
		wg.Add(1)
		go func(job ap.Work) {
			defer wg.Done()
			var lastOperation *af.Operation
			parentObserver, _ := ctx.Value(operationContextKey{}).(operationContext)
			jobCtx := WithOperationObserver(ctx, func(o af.Operation) {
				copy := o
				lastOperation = &copy
				if parentObserver.observe != nil {
					parentObserver.observe(o)
				}
			})
			jobCtx = workflowOperationContext(jobCtx, string(job.Task), job.Date)

			var e error
			switch job.Task {
			case ap.Migration:
				e = w.pipelineMigration(jobCtx, g, job)
			case ap.Organization:
				e = w.organizePipelinePage(jobCtx, g, job)
			case ap.Verification:
				e = w.pipelineVerification(jobCtx, g, remaining, job, immutable)
			case ap.Collection:
				_, e = w.pipelineCollection(jobCtx, g)
			}
			saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			saveErr := w.pipelineTransaction(saveCtx, g, func(tx *sql.Tx, p *pipelineCheckpoint) error {
				progress := p.Progress[job.Task]
				progress.UpdatedAt = time.Now().UTC()
				if lastOperation != nil {
					done := progress.UpdatedAt
					lastOperation.FinishedAt = &done
					lastOperation.State = "completed"
					if e != nil {
						lastOperation.State = "failed"
					}
					progress.Operation = lastOperation
				}
				progress.Diagnostic = nil

				if job.Task == ap.Organization {
					progress.AfterID = p.AfterID
				}
				if e != nil {
					d := Diagnose(e)
					d.Operation = lastOperation
					retry := time.Now().UTC().Add(30 * time.Second)
					d.RetryAt = &retry
					progress.Diagnostic = &d
				}
				p.Progress[job.Task] = progress
				if e != nil {
					p.Errors[job.Task] = Diagnose(e).Code
					// Only deterministic row errors skip an organization date. Connectivity,
					// permissions, missing indexes and timeouts retain the task for retry.
					if job.Task == ap.Organization && (Diagnose(e).Code == "invalid_source_row" || Diagnose(e).Code == "row_too_large") {
						if active, ok := p.State.Active[job.Task]; ok && active == job && p.State.Days[job.Date].Revision == job.Revision {
							if err := p.State.Finish(job, Diagnose(e).Code, ""); err != nil {
								return err
							}
							p.State.Days[job.Date].Result.Diagnostic = progress.Diagnostic
							p.AfterID = 0
							p.AfterCreated = 0
						}
					}
				} else {
					delete(p.Errors, job.Task)
				}
				if job.Task == ap.Collection {
					if active, ok := p.State.Active[ap.Collection]; ok && active.Token == job.Token {
						return p.State.Release(active)
					}
				}
				return nil
			})
			saveCancel()
			if saveErr != nil {
				resultMu.Lock()
				failures = append(failures, saveErr)
				resultMu.Unlock()
			}
		}(job)
	}
	wg.Wait()
	if len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	statusCtx, statusCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer statusCancel()
	return w.PipelineProgress(statusCtx)
}

func (w *Worker) pipelineMigration(ctx context.Context, g af.WriterGrant, job ap.Work) error {
	ready, indexErr := w.preparePipelineIndex(ctx, g)
	if indexErr != nil {
		return indexErr
	}
	if !ready {
		return nil
	}
	s, err := loadWorkflow(ctx, w.target)
	if err != nil {
		return err
	}
	if strings.HasPrefix(s.Phase, "reset_") {
		if err = w.importWorkflowPage(ctx, g, &s); err != nil {
			return err
		}
	}
	if strings.HasPrefix(s.Phase, "reset_") {
		return nil
	}
	return w.pipelineTransaction(ctx, g, func(tx *sql.Tx, p *pipelineCheckpoint) error { return p.State.Finish(job, "", "") })
}

func (w *Worker) pipelineVerification(ctx context.Context, g af.WriterGrant, remaining time.Duration, job ap.Work, immutable bool) error {
	err := w.pipelineTransaction(ctx, g, func(tx *sql.Tx, p *pipelineCheckpoint) error {
		if p.VerificationToken == job.Token {
			return nil
		}
		if p.State.Active[ap.Verification] != job {
			return ap.ErrConflict
		}
		s, e := loadWorkflow(ctx, tx)
		if e != nil {
			return e
		}
		id, e := newArchiveID()
		if e != nil {
			return e
		}
		policy := af.DefaultCoveragePolicy()
		policy.Budget = w.readBudget()
		policy.CoverageFrom = job.Date
		policy.Evidence = "archive_collection_boundary_not_proof_of_retention"
		if immutable {
			policy.SourceRetainedFrom = job.Date
			policy.Evidence = "operator_full_history_retained_and_immutable"
		}
		s.Date = job.Date
		s.Origin = job.Date
		s.Group = []string{job.Date}
		s.Verified = map[string]uint64{}
		s.CurrentScan = nil
		s.Verify = nil
		s.Seal = nil
		s.Scan = &af.BackfillTask{Identity: g.Identity, TaskID: id, Date: job.Date, Type: "date_backfill", Attempt: 1, Policy: policy}
		s.Phase = "backfill"
		p.VerificationToken = job.Token
		return saveWorkflow(ctx, tx, s)
	})
	if err != nil {
		return err
	}
	s, err := loadWorkflow(ctx, w.target)
	if err != nil {
		return err
	}
	if s.Phase != "live" {
		if _, err = w.WorkflowPass(ctx, g, remaining, immutable); err != nil {
			return err
		}
	}
	return w.pipelineTransaction(ctx, g, func(tx *sql.Tx, p *pipelineCheckpoint) error {
		s, e := loadWorkflow(ctx, tx)
		if e != nil {
			return e
		}
		if s.Phase != "live" {
			return nil
		}
		active, ok := p.State.Active[ap.Verification]
		if !ok || active.Token != job.Token {
			return ap.ErrConflict
		}
		version := ""
		if s.ErrorCode == "" {
			var id sql.NullString
			if e = tx.QueryRowContext(ctx, `SELECT HEX(current_version_id) FROM archive_days WHERE log_date=?`, job.Date).Scan(&id); e != nil {
				return e
			}
			if !id.Valid {
				return ErrWriterCheckpoint
			}
			version = id.String
		}
		if e = p.State.Finish(active, s.ErrorCode, version); e != nil {
			return e
		}
		p.VerificationToken = 0
		return nil
	})
}

func pipelineRetryReady(p *pipelineCheckpoint, task ap.Task) bool {
	d := p.Progress[task].Diagnostic
	return d == nil || d.RetryAt == nil || !time.Now().Before(*d.RetryAt)
}
