package main

import (
	"context"
	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
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
	if err := worker.AcquireWorkflow(passCtx, g, remaining); err != nil {
		v.incrementalFailure(st, out.Config, err, false)
		return
	}
	progress, err := worker.WorkflowPass(passCtx, g, remaining, out.Config.HistoryImmutable)
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
		return
	}
	if result, e := w.ProgressV2(passCtx, g.Identity); e == nil {
		setArchiveV2Progress(st, result)
	}
	st.State = "running"
	st.Error = ""
	v.failures = 0
	// Every phase obeys the operator's interval, including historical pages and verification.
	v.nextPass = time.Now().Add(time.Duration(out.Config.IntervalSeconds) * time.Second)
}
