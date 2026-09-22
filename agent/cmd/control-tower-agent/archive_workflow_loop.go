package main

import (
	"context"
	"controltower/agent/internal/logarchive"
	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
	ap "controltower/internal/archivepipeline"
	"log"
	"sync"
	"time"
)

type wholeArchiveWorker interface {
	AcquireWorkflow(context.Context, af.WriterGrant, time.Duration) error
	WorkflowPass(context.Context, af.WriterGrant, time.Duration, bool) (*af.WorkflowStatus, error)
}

func (v *archiveV2Runner) runWholeArchive(ctx context.Context, w atomicArchiveWorker, st *ac.Status, out ac.Response, g af.WriterGrant, remaining time.Duration) {
	worker, ok := w.(wholeArchiveWorker)
	if !ok {
		st.State = "error"
		st.Error = "archive Agent does not support full history workflow"
		return
	}
	budget := af.DefaultScanBudget()
	budget.MaxRows = out.Config.BatchSize
	w.SetScanBudget(budget)
	w.SetBatchSize(out.Config.BatchSize)
	w.WithDelay(time.Duration(out.Config.DelaySeconds) * time.Second)
	limit := 30 * time.Second
	if remaining-5*time.Second < limit {
		limit = remaining - 5*time.Second
	}
	passCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	st.State, st.Error, st.Diagnostic = "running", "", nil
	st.Foundation.WriterEpoch = g.WriterEpoch
	var observerMu sync.Mutex
	observe := func(operation af.Operation) {
		observerMu.Lock()
		defer observerMu.Unlock()
		st.Operation = &operation
		if v.publish != nil {
			v.publish(*st)
		}
	}
	passCtx = logarchive.WithOperationObserver(passCtx, observe)
	observe(af.Operation{Phase: "acquire", Code: "acquire_writer", Database: "archive", Table: "archive_dataset_meta", State: "executing", StartedAt: time.Now().UTC()})
	if err := worker.AcquireWorkflow(passCtx, g, remaining); err != nil {
		v.incrementalFailure(st, out.Config, err, false)
		v.workflowFailure(st, err)
		return
	}
	var progress *af.WorkflowStatus
	var err error
	if out.Config.Pipeline != nil {
		if pipeline, okay := w.(interface {
			PipelinePass(context.Context, af.WriterGrant, time.Duration, ap.Settings, bool) (*ap.Status, error)
			WorkflowProgress(context.Context) (*af.WorkflowStatus, error)
		}); okay {
			st.Pipeline, err = pipeline.PipelinePass(passCtx, g, remaining, *out.Config.Pipeline, out.Config.HistoryImmutable)
			if err == nil {
				progress, err = pipeline.WorkflowProgress(passCtx)
			}
		} else {
			err = af.ErrConflict
		}
	} else {
		progress, err = worker.WorkflowPass(passCtx, g, remaining, out.Config.HistoryImmutable)
	}
	v.claimed = &g
	st.Foundation.WriterEpoch = g.WriterEpoch
	st.Backfill = nil
	st.Reconcile = nil
	st.Seal = nil
	if progress != nil {
		st.Workflow = progress
	}
	if err != nil {
		v.incrementalFailure(st, out.Config, err, false)
		v.workflowFailure(st, err)
		return
	}
	finishWorkflowOperation(st, "completed")
	if result, e := w.ProgressV2(passCtx, g.Identity); e == nil {
		setArchiveV2Progress(st, result)
	}
	st.State = "running"
	st.Error = ""
	v.failures = 0
	// Every phase obeys the operator's interval, including historical pages and verification.
	v.nextPass = time.Now().Add(time.Duration(out.Config.IntervalSeconds) * time.Second)
}

func finishWorkflowOperation(st *ac.Status, state string) {
	if st.Operation == nil {
		return
	}
	o := *st.Operation
	t := time.Now().UTC()
	o.State, o.FinishedAt = state, &t
	st.Operation = &o
}

func (v *archiveV2Runner) workflowFailure(st *ac.Status, err error) {
	finishWorkflowOperation(st, "failed")
	d := logarchive.Diagnose(err)
	d.Operation = cloneArchiveOperation(st.Operation)
	retry := v.nextPass.UTC()
	if retry.Before(d.OccurredAt) {
		retry = d.OccurredAt
	}
	d.RetryAt = &retry
	st.Diagnostic = &d
	st.Error = d.Code
	// Never log driver text, SQL, credentials or archived customer content.
	if d.Operation != nil {
		log.Printf("log archive: phase=%s operation=%s database=%s table=%s date=%s code=%s mysql=%d sqlstate=%s row_id=%d row_bytes=%d retry_at=%s", d.Operation.Phase, d.Operation.Code, d.Operation.Database, d.Operation.Table, d.Operation.Date, d.Code, d.MySQLNumber, d.SQLState, d.RowID, d.RowBytes, retry.Format(time.RFC3339))
	}
}
