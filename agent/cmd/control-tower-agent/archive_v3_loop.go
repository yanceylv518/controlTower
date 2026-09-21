package main

import (
	"context"
	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
	"errors"
	"time"
)

func archiveScanTask(out ac.Response, st *ac.Status, grant af.WriterGrant) (af.BackfillTask, bool) {
	if out.BackfillTask == nil || st.Foundation == nil || !st.Foundation.SupportsBackfill() {
		return af.BackfillTask{}, false
	}
	task := *out.BackfillTask
	if task.Validate() != nil || !task.Identity.Equal(grant.Identity) {
		return af.BackfillTask{}, false
	}
	if previous := st.Backfill; previous != nil && previous.TaskID == task.TaskID && previous.Attempt == task.Attempt && previous.WriterEpoch == grant.WriterEpoch && (previous.State == "succeeded" || previous.State == "blocked" || (previous.State == "retry_wait" && time.Now().Unix() < previous.RetryAfterUnix)) {
		// A cached dispatch cannot reopen a durable terminal result. CT must
		// acknowledge it and dispatch a retry attempt or a different task.
		return af.BackfillTask{}, false
	}
	return task, true
}

func (v *archiveV2Runner) runArchivePass(ctx context.Context, w atomicArchiveWorker, st *ac.Status, out ac.Response, grant af.WriterGrant, remaining time.Duration) {
	if out.Config.FullHistory { v.runWholeArchive(ctx, w, st, out, grant, remaining); return }
	policy := af.DefaultCoveragePolicy()
	if out.ArchivePolicy != nil {
		if out.ArchivePolicy.Validate() != nil {
			v.release(ctx, w)
			st.State, st.Error = "error", "archive budget policy is invalid"
			return
		}
		policy = *out.ArchivePolicy
	}
	task, canScan := archiveScanTask(out, st, grant)
	p4policy, canVerify := archiveVerificationTask(out, st, grant)
	canScan = canScan || canVerify
	canScan = canScan && !time.Now().Before(v.nextScan)
	canIncrement := !time.Now().Before(v.nextIncremental)
	runScan := canScan && (v.incrementalTurns >= 3 || !canIncrement)
	if !runScan && !canIncrement {
		st.State = "error"
		return
	}
	if runScan {
		policy = task.Policy
		if canVerify {
			policy = p4policy
		}
	}
	if policy.Budget.MaxRows > out.Config.BatchSize {
		policy.Budget.MaxRows = out.Config.BatchSize
	}
	w.SetScanBudget(policy.Budget)
	w.SetBatchSize(out.Config.BatchSize)
	w.WithDelay(time.Duration(out.Config.DelaySeconds) * time.Second)
	budget := remaining - 5*time.Second
	if limit := time.Duration(policy.Budget.MaxDurationMillis) * time.Millisecond; budget > limit {
		budget = limit
	}
	passCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if err := w.AcquireWriter(passCtx, grant, remaining); err != nil {
		// Both streams need the same writer authorization. A pending date
		// cannot bypass connection/lease backoff by taking the next slot.
		v.incrementalFailure(st, out.Config, err, false)
		v.nextScan = v.nextIncremental
		return
	}
	v.claimed = &grant
	st.Foundation.WriterEpoch = grant.WriterEpoch
	if st.Backfill != nil && st.Backfill.WriterEpoch != grant.WriterEpoch {
		st.Backfill = nil
	}
	if st.Reconcile != nil && st.Reconcile.WriterEpoch != grant.WriterEpoch {
		st.Reconcile = nil
	}
	if st.Seal != nil && st.Seal.WriterEpoch != grant.WriterEpoch {
		st.Seal = nil
	}
	if runScan {
		if canVerify {
			err := runArchiveVerification(passCtx, w, st, out, grant)
			v.incrementalTurns = 0
			v.nextPass = time.Now().Add(2 * time.Second)
			if err != nil {
				v.scanFailures++
				if v.scanFailures > 4 {
					v.scanFailures = 4
				}
				v.nextScan = time.Now().Add(time.Duration(15*(1<<v.scanFailures)) * time.Second)
			} else {
				v.scanFailures = 0
				v.nextScan = time.Time{}
			}
			if v.failures == 0 {
				st.State, st.Error = "running", ""
			}
			return
		}
		// A single synchronous operation shares the target fence with the
		// incremental writer. Backfill keeps its own cursor and retry budget.
		result, err := w.ScanDateV3(passCtx, grant, task)
		if result.Validate() == nil && result.TaskID == task.TaskID && result.Attempt == task.Attempt && result.WriterEpoch == grant.WriterEpoch && result.Date == task.Date {
			st.Backfill = &result
			st.Metrics = &af.ArchiveMetrics{Stream: task.Type, ReadBytes: result.ReadBytes, WrittenBytes: result.WrittenBytes, ElapsedMillis: result.ElapsedMillis, ErrorCode: result.ErrorCode}
		} else {
			st.Metrics = &af.ArchiveMetrics{Stream: task.Type, ErrorCode: "scan_failed"}
			if err == nil {
				err = errors.New("invalid archive scan result")
			}
		}
		v.incrementalTurns = 0
		v.nextPass = time.Now().Add(2 * time.Second)
		if err != nil {
			v.scanFailures++
			if v.scanFailures > 4 {
				v.scanFailures = 4
			}
			v.nextScan = time.Now().Add(time.Duration(15*(1<<v.scanFailures)) * time.Second)
		} else {
			v.scanFailures = 0
			v.nextScan = time.Time{}
		}
		if v.failures == 0 {
			st.State, st.Error = "running", ""
		}
		return
	}
	result, err := w.PassV2(passCtx, grant)
	v.incrementalTurns++
	if result.Metrics.Validate() == nil {
		metrics := result.Metrics
		st.Metrics = &metrics
	}
	if err != nil {
		v.incrementalFailure(st, out.Config, err, canScan)
		switch result.Metrics.ErrorCode {
		case "row_too_large":
			st.Error = "archive row exceeds the configured byte limit; cursor was not skipped"
		case "invalid_timestamp", "invalid_source_row":
			st.Error = "archive source row is invalid; inspect the recorded issue before retrying"
		case "schema_changed":
			st.Error = "archive source or target schema changed; preparation must be verified"
		}
		return
	}
	setArchiveV2Progress(st, result)
	st.LastBatchRows = result.Rows
	v.failures = 0
	v.nextIncremental = time.Time{}
	st.State, st.Error = "running", ""
	delay := time.Duration(out.Config.IntervalSeconds) * time.Second
	if result.More {
		delay = 2 * time.Second
	} else {
		// Once caught up, give the next reserved slot to a pending date scan.
		v.incrementalTurns = 3
	}
	v.nextPass = time.Now().Add(delay)
}

func (v *archiveV2Runner) incrementalFailure(st *ac.Status, config ac.Config, err error, canScan bool) {
	v.failures++
	if v.failures > 4 {
		v.failures = 4
	}
	st.State, st.Error = "error", archiveWriterError(err)
	delay := time.Duration(15*(1<<v.failures)) * time.Second
	if configured := time.Duration(config.IntervalSeconds) * time.Second; delay < configured {
		delay = configured
	}
	v.nextIncremental = time.Now().Add(delay)
	if canScan {
		delay = 2 * time.Second
	}
	v.nextPass = time.Now().Add(delay)
}
