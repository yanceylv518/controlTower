package main

import (
	"context"
	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
	"errors"
	"testing"
	"time"
)

type workflowLoopWorker struct {
	atomicLoopWorker
	phase string
	err   error
}

func (w *workflowLoopWorker) AcquireWorkflow(context.Context, af.WriterGrant, time.Duration) error {
	return nil
}
func (w *workflowLoopWorker) WorkflowPass(context.Context, af.WriterGrant, time.Duration, bool) (*af.WorkflowStatus, error) {
	return &af.WorkflowStatus{Phase: w.phase}, w.err
}

func TestWorkflowErrorAndOperationSnapshots(t *testing.T) {
	_, st, out := validV2Control()
	var snapshots []ac.Status
	v := archiveV2Runner{publish: func(s ac.Status) { snapshots = append(snapshots, cloneArchiveStatus(s)) }}
	w := &workflowLoopWorker{phase: "import_target", err: context.DeadlineExceeded}
	v.runWholeArchive(context.Background(), w, &st, out, *out.WriterGrant, 90*time.Second)
	if len(snapshots) == 0 || snapshots[0].Operation.State != "executing" || st.Operation.State != "failed" || st.Diagnostic.Code != "operation_timeout" || st.Diagnostic.RetryAt == nil {
		t.Fatalf("missing failure evidence %+v", st)
	}
	snapshot := cloneArchiveStatus(st)
	snapshot.Diagnostic.Operation.Table = "changed"
	*snapshot.Diagnostic.RetryAt = time.Time{}
	if st.Diagnostic.Operation.Table == "changed" || st.Diagnostic.RetryAt.IsZero() {
		t.Fatal("mutable diagnostic shared with poll")
	}
	w.err = nil
	v.runWholeArchive(context.Background(), w, &st, out, *out.WriterGrant, 90*time.Second)
	if st.Diagnostic != nil || st.Error != "" || st.Operation.State != "completed" {
		t.Fatal("successful retry retains active failure")
	}
}

func TestWorkflowEveryPhaseHonorsConfiguredInterval(t *testing.T) {
	for _, phase := range []string{"reset_state", "reset_daily", "reset_monthly", "import_target", "backfill", "verify", "seal", "live"} {
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

func TestWorkflowPreparationSnapshotIsIndependent(t *testing.T) {
	_, st, _ := validV2Control()
	st.Workflow = &af.WorkflowStatus{Phase: "reset_state", Preparation: &af.WorkflowPreparationProgress{ProcessedRows: 1000}}
	snapshot := cloneArchiveStatus(st)
	snapshot.Workflow.Preparation.ProcessedRows = 2000
	if st.Workflow.Preparation.ProcessedRows != 1000 {
		t.Fatal("poll snapshot mutated writer progress")
	}
}

type workflowDailyWorker struct {
	atomicLoopWorker
	reads int
	after string
	err   error
}

func (w *workflowDailyWorker) WorkflowDayPage(_ context.Context, after string) (*af.WorkflowDayPage, error) {
	w.reads++
	w.after = after
	return &af.WorkflowDayPage{NextAfter: "2026-09-01", Days: []af.WorkflowDay{{Counts: &af.WorkflowDayCounts{LogRows: "5"}}}}, w.err
}
func TestWorkflowDailyReadWhilePausedDoesNotWriteArchive(t *testing.T) {
	cfg, st, out := validV2Control()
	out.Config.Running = false
	out.Config.FullHistory = true
	w := &workflowDailyWorker{}
	v := archiveV2Runner{}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(time.Minute), nil)
	if w.reads != 1 || w.passes != 0 || w.acquires != 0 || st.State != "paused" || st.WorkflowDaily == nil {
		t.Fatalf("paused read wrote or disappeared: %+v %+v", w, st)
	}
	snapshot := cloneArchiveStatus(st)
	snapshot.WorkflowDaily.Days[0].Counts.LogRows = "6"
	if st.WorkflowDaily.Days[0].Counts.LogRows != "5" {
		t.Fatal("daily snapshot shared with writer")
	}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(time.Minute), nil)
	if w.reads != 1 {
		t.Fatal("daily reads not throttled")
	}
	v.nextCheck = time.Time{}
	w.err = errors.New("fixture")
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(time.Minute), nil)
	if w.after != "2026-09-01" || st.WorkflowDailyError == "" || st.State != "paused" || st.WorkflowDaily == nil {
		t.Fatal("read failure lost snapshot or changed execution", st)
	}
}
