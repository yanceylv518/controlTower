package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
)

func readyScan(st *ac.Status, out *ac.Response) af.BackfillStatus {
	st.Foundation.Capabilities = append(st.Foundation.Capabilities, af.CapabilityBackfill)
	task := af.BackfillTask{Identity: st.Foundation.Identity, TaskID: strings.Repeat("e", 32), Date: "2026-09-18", Type: "date_backfill", Attempt: 1, Policy: af.DefaultCoveragePolicy()}
	out.BackfillTask = &task
	return af.BackfillStatus{TaskID: task.TaskID, Date: task.Date, Attempt: 1, WriterEpoch: out.WriterGrant.WriterEpoch, State: "running"}
}

func TestArchiveV3ReservesDateScanDuringIncrementalBacklog(t *testing.T) {
	cfg, st, out := validV2Control()
	w := &atomicLoopWorker{scanResult: readyScan(&st, &out)}
	w.result.More = true
	v := archiveV2Runner{}
	for turn := 0; turn < 8; turn++ {
		v.nextPass = time.Time{}
		v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	}
	if w.passes != 6 || w.scans != 2 || w.acquires != 8 {
		t.Fatalf("expected one scan slot per three incremental batches: incremental=%d scans=%d acquisitions=%d", w.passes, w.scans, w.acquires)
	}
	if st.LastID != 0 || st.Backfill == nil {
		t.Fatal("scan failed to report its own progress or changed the incremental cursor")
	}
}

func TestArchiveV3BackfillFailureDoesNotStopIncremental(t *testing.T) {
	cfg, st, out := validV2Control()
	w := &atomicLoopWorker{scanResult: readyScan(&st, &out), scanErr: errors.New("source unavailable")}
	w.scanResult.State, w.scanResult.ErrorCode = "retry_wait", "source_unavailable"
	v := archiveV2Runner{incrementalTurns: 3}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	v.nextPass = time.Time{}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if w.scans != 1 || w.passes != 1 || st.Backfill == nil || st.Backfill.State != "retry_wait" || st.State != "running" {
		t.Fatal("a failed scan must retain task evidence and leave incremental progress available")
	}
}

func TestArchiveV3IncrementalFailureDoesNotStarveBackfill(t *testing.T) {
	cfg, st, out := validV2Control()
	w := &atomicLoopWorker{scanResult: readyScan(&st, &out), passErr: errors.New("oversized incremental row")}
	v := archiveV2Runner{}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	v.nextPass = time.Time{}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if w.passes != 1 || w.scans != 1 || st.State != "error" {
		t.Fatal("incremental backoff must yield a scan slot while retaining its failure")
	}
}

func TestArchiveV3CachedTerminalTaskCannotRestartButNewAttemptCan(t *testing.T) {
	cfg, st, out := validV2Control()
	result := readyScan(&st, &out)
	result.State, result.EmptyCandidate = "succeeded", true
	result.BatchID = strings.Repeat("f", 32)
	_, result.SourceNowUnix, _ = af.DateBounds(result.Date)
	st.Foundation.WriterEpoch = result.WriterEpoch
	st.Backfill = &result
	w := &atomicLoopWorker{scanResult: result}
	v := archiveV2Runner{incrementalTurns: 3}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if w.scans != 0 || w.passes != 1 {
		t.Fatal("cached terminal dispatch reopened a finished task")
	}
	out.BackfillTask.Attempt++
	w.scanResult.Attempt++
	v.nextPass = time.Time{}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if w.scans != 1 {
		t.Fatal("explicit next attempt was not allowed")
	}
}

func TestArchiveV3PauseAndNewEpochKeepPollStatusValid(t *testing.T) {
	cfg, st, out := validV2Control()
	result := readyScan(&st, &out)
	st.Foundation.WriterEpoch = result.WriterEpoch
	st.Backfill = &result
	w := &atomicLoopWorker{scanResult: result}
	v := archiveV2Runner{incrementalTurns: 3}
	out.Config.Running = false
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if w.acquires != 0 || w.scans != 0 || st.State != "paused" {
		t.Fatal("pause must not claim or execute a scan")
	}
	out.Config.Running = true
	out.WriterGrant.WriterEpoch++
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if st.Backfill != nil || st.Foundation.WriterEpoch != out.WriterGrant.WriterEpoch || !st.Validate() {
		t.Fatal("old scan epoch poisoned subsequent control polls")
	}
}

func TestArchiveV3StatusSnapshotOwnsScanAndMetricPointers(t *testing.T) {
	_, st, out := validV2Control()
	result := readyScan(&st, &out)
	st.Backfill = &result
	lag := int64(5)
	st.Metrics = &af.ArchiveMetrics{Stream: "incremental", LagSeconds: &lag}
	snapshot := cloneArchiveStatus(st)
	st.Backfill.State = "blocked"
	*st.Metrics.LagSeconds = 999
	if snapshot.Backfill.State != "running" || *snapshot.Metrics.LagSeconds != 5 {
		t.Fatal("poll snapshot shares mutable scan data")
	}
}

func TestArchiveV3RejectsForeignTaskAndHonorsRowBudget(t *testing.T) {
	cfg, st, out := validV2Control()
	w := &atomicLoopWorker{scanResult: readyScan(&st, &out)}
	out.BackfillTask.DatasetID = strings.Repeat("9", 32)
	policy := af.DefaultCoveragePolicy()
	policy.Budget.MaxRows = 12
	out.ArchivePolicy = &policy
	v := archiveV2Runner{incrementalTurns: 3}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if w.scans != 0 || w.passes != 1 || w.budget.MaxRows != 12 {
		t.Fatal("foreign task ran or configured budget was ignored")
	}
}

func TestArchiveV3RetryWaitCanResumeSameAttempt(t *testing.T) {
	cfg, st, out := validV2Control()
	result := readyScan(&st, &out)
	result.State, result.ErrorCode = "retry_wait", "source_unavailable"
	result.RetryAfterUnix = time.Now().Add(-time.Second).Unix()
	st.Backfill = &result
	st.Foundation.WriterEpoch = result.WriterEpoch
	w := &atomicLoopWorker{scanResult: result}
	w.scanResult.State, w.scanResult.ErrorCode = "running", ""
	v := archiveV2Runner{incrementalTurns: 3}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if w.scans != 1 || st.Backfill.State != "running" || st.Backfill.Attempt != result.Attempt {
		t.Fatal("expired retry wait must resume the same authoritative task cursor")
	}
}

func TestArchiveV3WriterFailureBacksOffBothStreams(t *testing.T) {
	cfg, st, out := validV2Control()
	w := &atomicLoopWorker{scanResult: readyScan(&st, &out), acquireErr: errors.New("target unavailable")}
	v := archiveV2Runner{incrementalTurns: 3}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if time.Until(v.nextPass) < 25*time.Second || time.Until(v.nextScan) < 25*time.Second {
		t.Fatal("pending scan bypassed shared writer backoff")
	}
	v.nextPass = time.Time{}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if w.acquires != 1 || w.scans != 0 || w.passes != 0 {
		t.Fatal("writer retry bypassed the backoff for the other stream")
	}
}

func TestArchiveV3InvalidScanResultCannotPolluteStatus(t *testing.T) {
	cfg, st, out := validV2Control()
	readyScan(&st, &out)
	w := &atomicLoopWorker{}
	v := archiveV2Runner{incrementalTurns: 3}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if st.Backfill != nil || v.scanFailures != 1 || st.Metrics == nil || st.Metrics.ErrorCode != "scan_failed" || time.Until(v.nextScan) < 25*time.Second {
		t.Fatal("invalid result was treated as success or lost failure backoff")
	}
}
