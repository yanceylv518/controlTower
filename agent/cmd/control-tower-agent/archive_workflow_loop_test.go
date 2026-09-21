package main

import (
	"context"
	af "controltower/internal/archivecontract"
	"testing"
	"time"
)

type workflowLoopWorker struct {
	atomicLoopWorker
	phase string
}

func (w *workflowLoopWorker) AcquireWorkflow(context.Context, af.WriterGrant, time.Duration) error {
	return nil
}
func (w *workflowLoopWorker) WorkflowPass(context.Context, af.WriterGrant, time.Duration, bool) (*af.WorkflowStatus, error) {
	return &af.WorkflowStatus{Phase: w.phase}, nil
}

func TestWorkflowEveryPhaseHonorsConfiguredInterval(t *testing.T) {
	for _, phase := range []string{"import_target", "backfill", "verify", "seal", "live"} {
		t.Run(phase, func(t *testing.T) {
			_, st, out := validV2Control()
			out.Config.IntervalSeconds = 90
			w := &workflowLoopWorker{phase: phase}
			v := archiveV2Runner{}
			before := time.Now()
			v.runWholeArchive(context.Background(), w, &st, out, *out.WriterGrant, 90*time.Second)
			if v.nextPass.Before(before.Add(90 * time.Second)) {
				t.Fatal("workflow accelerated past configured interval")
			}
			if w.passes != 0 {
				t.Fatal("workflow also invoked legacy incremental scan")
			}
		})
	}
}
