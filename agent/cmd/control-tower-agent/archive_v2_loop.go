package main

import (
	"context"
	"controltower/agent/internal/config"
	"controltower/agent/internal/logarchive"
	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
	ap "controltower/internal/archivepipeline"
	"errors"
	"log"
	"maps"
	"time"
)

type atomicArchiveWorker interface {
	InspectFoundation(context.Context, af.Identity) (logarchive.FoundationInfo, error)
	AcquireWriter(context.Context, af.WriterGrant, time.Duration) error
	ReleaseWriter(context.Context, af.WriterGrant) error
	PassV2(context.Context, af.WriterGrant) (logarchive.BatchResult, error)
	ProgressV2(context.Context, af.Identity) (logarchive.BatchResult, error)
	ScanDateV3(context.Context, af.WriterGrant, af.BackfillTask) (af.BackfillStatus, error)
	SetScanBudget(af.ScanBudget)
	SetBatchSize(int)
	WithDelay(time.Duration) *logarchive.Worker
}

type archiveV2Runner struct {
	publish             func(ac.Status)
	dailyAfter          string
	nextCheck, nextPass time.Time
	claimed             *af.WriterGrant
	failures            int
	scanFailures        int
	incrementalTurns    int
	nextIncremental     time.Time
	nextScan            time.Time
}

func archiveWriterGrant(out ac.Response, cfg config.Config, session string, expires, now time.Time) (af.WriterGrant, time.Duration, bool) {
	if out.WriterGrant == nil || !out.Granted || !out.Config.Running || !out.Config.Validate() || out.Config.ReconcileID != "" || out.SiteID != cfg.LogArchiveIdentity.SiteID || out.Config.AgentID != cfg.AgentID || out.Config.InstanceID != cfg.InstanceID {
		return af.WriterGrant{}, 0, false
	}
	g := *out.WriterGrant
	if g.Validate() != nil || !g.Identity.Equal(cfg.LogArchiveIdentity) || g.Session != session || g.ConfigVersion != out.Config.Version || g.LeaseSeconds != out.LeaseSeconds {
		return af.WriterGrant{}, 0, false
	}
	remaining := expires.Sub(now) - 30*time.Second
	if remaining <= 5*time.Second {
		return af.WriterGrant{}, 0, false
	}
	if remaining > 90*time.Second {
		return af.WriterGrant{}, 0, false
	}
	return g, remaining, true
}

func (v *archiveV2Runner) release(ctx context.Context, w atomicArchiveWorker) {
	if v.claimed == nil || w == nil {
		return
	}
	releaseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	_ = w.ReleaseWriter(releaseCtx, *v.claimed)
	cancel()
	v.claimed = nil
}

func setArchiveV2Progress(st *ac.Status, result logarchive.BatchResult) {
	st.LastID = result.AfterID
	st.Foundation.CatalogRevision = result.CatalogRevision
	if result.BatchID != "" && st.Foundation.WriterEpoch > 0 {
		st.Foundation.ReceiptID = result.BatchID
	}
	if !result.CommittedAt.IsZero() {
		t := result.CommittedAt.UTC()
		st.LastSuccess = &t
		st.VerifiedAt = &t
		st.VerifiedRows = result.Rows
	}
}

func (v *archiveV2Runner) step(ctx context.Context, w atomicArchiveWorker, cfg config.Config, st *ac.Status, out ac.Response, expires time.Time, controlErr error) {
	if w == nil {
		st.Configured = false
		st.State = "error"
		st.Error = "archive connection configuration unavailable"
		return
	}
	now := time.Now()
	if now.After(v.nextCheck) {
		checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		_, err := w.InspectFoundation(checkCtx, cfg.LogArchiveIdentity)
		if err == nil {
			var progress logarchive.BatchResult
			progress, err = w.ProgressV2(checkCtx, cfg.LogArchiveIdentity)
			if errors.Is(err, logarchive.ErrWriterLegacyData) {
				if _, supported := w.(wholeArchiveWorker); supported {
					err = nil
				}
			}
			if err == nil {
				setArchiveV2Progress(st, progress)
			}
		}
		cancel()
		if err == nil && out.Config.Pipeline != nil {
			if reader, ok := w.(interface {
				PipelineProgress(context.Context) (*ap.Status, error)
			}); ok {
				readCtx, readCancel := context.WithTimeout(ctx, 3*time.Second)
				pipeline, readErr := reader.PipelineProgress(readCtx)
				readCancel()
				if readErr == nil {
					st.Pipeline = pipeline
				}
			}
		}
		if err == nil && out.Config.FullHistory {
			if reader, ok := w.(interface {
				WorkflowDayPage(context.Context, string) (*af.WorkflowDayPage, error)
			}); ok {
				dailyCtx, dailyCancel := context.WithTimeout(ctx, 5*time.Second)
				page, dailyErr := reader.WorkflowDayPage(dailyCtx, v.dailyAfter)
				dailyCancel()
				if dailyErr != nil {
					d := logarchive.Diagnose(dailyErr)
					st.WorkflowDailyError = d.Code
					log.Printf("log archive: daily status read failed code=%s mysql=%d sqlstate=%s", d.Code, d.MySQLNumber, d.SQLState)
				} else {
					st.WorkflowDaily, st.WorkflowDailyError = page, ""
					if page != nil {
						v.dailyAfter = page.NextAfter
					}
				}
			}
		}
		st.Configured = err == nil
		if err != nil {
			d := logarchive.Diagnose(err)
			st.Diagnostic = &d
			st.Error = "archive identity/schema/checkpoint preflight failed"
			if errors.Is(err, logarchive.ErrWriterLegacyData) || errors.Is(err, logarchive.ErrWriterCheckpoint) || errors.Is(err, logarchive.ErrWriterReceipt) {
				st.Error = archiveWriterError(err)
			}
		} else if v.failures == 0 {
			st.Error = ""
		}
		v.nextCheck = time.Now().Add(30 * time.Second)
	}
	if !st.Configured {
		v.release(ctx, w)
		st.State = "error"
		return
	}
	if controlErr != nil {
		v.release(ctx, w)
		st.State = "waiting"
		st.Error = "archive control unavailable"
		return
	}
	if out.SiteID != st.SiteID || out.Config.Version < st.AppliedVersion {
		v.release(ctx, w)
		st.State = "waiting"
		return
	}
	st.AppliedVersion = out.Config.Version
	if !out.Config.Running {
		v.release(ctx, w)
		st.State = "paused"
		st.Error = ""
		v.failures = 0
		v.nextPass = time.Time{}
		v.scanFailures = 0
		v.incrementalTurns = 0
		v.nextIncremental = time.Time{}
		v.nextScan = time.Time{}
		return
	}
	grant, remaining, okay := archiveWriterGrant(out, cfg, st.Session, expires, time.Now())
	if !okay {
		v.release(ctx, w)
		st.State = "waiting"
		return
	}
	if v.claimed != nil && (v.claimed.WriterEpoch != grant.WriterEpoch || v.claimed.Session != grant.Session) {
		v.release(ctx, w)
	}
	if time.Now().Before(v.nextPass) {
		if v.failures > 0 {
			st.State = "error"
		} else {
			st.State = "running"
		}
		return
	}
	v.runArchivePass(ctx, w, st, out, grant, remaining)
}

func archiveWriterError(err error) string {
	switch {
	case errors.Is(err, logarchive.ErrWriterLegacyData):
		return "existing archive has no authoritative checkpoint; explicit import is required"
	case errors.Is(err, logarchive.ErrWriterFrozen):
		return "archive date is frozen; batch and checkpoint were not changed"
	case errors.Is(err, logarchive.ErrWriterLease):
		return "archive writer authorization expired or was superseded; waiting for a fresh grant"
	case errors.Is(err, logarchive.ErrWriterCheckpoint):
		return "archive target checkpoint is inconsistent; automatic cursor reset is disabled"
	case errors.Is(err, logarchive.ErrWriterReceipt):
		return "archive batch receipt conflicts with its payload; retry cannot overwrite it"
	default:
		return "archive batch failed; recovery will verify the authoritative target checkpoint"
	}
}

// Polling runs concurrently with the writer. Never share mutable status
// pointers or slices across that boundary (including after publication).
func cloneArchiveStatus(st ac.Status) ac.Status {
	st.Operation = cloneArchiveOperation(st.Operation)
	if st.Diagnostic != nil {
		d := *st.Diagnostic
		d.Operation = cloneArchiveOperation(d.Operation)
		if d.RetryAt != nil {
			t := *d.RetryAt
			d.RetryAt = &t
		}
		st.Diagnostic = &d
	}
	if st.Pipeline != nil {
		value := *st.Pipeline
		value.Active = maps.Clone(value.Active)
		value.Errors = maps.Clone(value.Errors)
		value.Progress = maps.Clone(value.Progress)
		for task, p := range value.Progress {
			p.Operation = cloneArchiveOperation(p.Operation)
			if p.Diagnostic != nil {
				d := *p.Diagnostic
				d.Operation = cloneArchiveOperation(d.Operation)
				p.Diagnostic = &d
			}
			value.Progress[task] = p
		}
		st.Pipeline = &value
	}
	if st.WorkflowDaily != nil {
		p := *st.WorkflowDaily
		p.Days = append([]af.WorkflowDay(nil), p.Days...)
		for i := range p.Days {
			if p.Days[i].Diagnostic != nil {
				d := *p.Days[i].Diagnostic
				d.Operation = cloneArchiveOperation(d.Operation)
				p.Days[i].Diagnostic = &d
			}
			if p.Days[i].Raw != nil {
				raw := *p.Days[i].Raw
				if raw.Rows != nil {
					value := *raw.Rows
					raw.Rows = &value
				}
				if raw.ObservedAt != nil {
					value := *raw.ObservedAt
					raw.ObservedAt = &value
				}
				p.Days[i].Raw = &raw
			}
			if p.Days[i].Counts != nil {
				counts := *p.Days[i].Counts
				p.Days[i].Counts = &counts
			}
			if p.Days[i].UpdatedAt != nil {
				updated := *p.Days[i].UpdatedAt
				p.Days[i].UpdatedAt = &updated
			}
		}
		st.WorkflowDaily = &p
	}
	if st.PrepareIdentity != nil {
		value := *st.PrepareIdentity
		st.PrepareIdentity = &value
	}
	if st.Prepared != nil {
		value := *st.Prepared
		st.Prepared = &value
	}
	if st.Workflow != nil {
		value := *st.Workflow
		value.Issues = append([]af.WorkflowIssue(nil), st.Workflow.Issues...)
		if value.Preparation != nil {
			progress := *value.Preparation
			value.Preparation = &progress
		}
		st.Workflow = &value
	}
	if st.Reconcile != nil {
		value := *st.Reconcile
		st.Reconcile = &value
	}
	if st.Seal != nil {
		value := *st.Seal
		value.Versions = append([]af.SealedDay(nil), value.Versions...)
		st.Seal = &value
	}
	if st.Backfill != nil {
		value := *st.Backfill
		st.Backfill = &value
	}
	if st.Metrics != nil {
		value := *st.Metrics
		if value.LagSeconds != nil {
			lag := *value.LagSeconds
			value.LagSeconds = &lag
		}
		st.Metrics = &value
	}
	if st.Foundation != nil {
		f := *st.Foundation
		f.Capabilities = append([]string(nil), f.Capabilities...)
		st.Foundation = &f
	}
	if st.Reconciliation != nil {
		r := *st.Reconciliation
		st.Reconciliation = &r
	}
	st.Days = append([]ac.Day(nil), st.Days...)
	return st
}

func cloneArchiveOperation(o *af.Operation) *af.Operation {
	if o == nil {
		return nil
	}
	v := *o
	if v.FinishedAt != nil {
		t := *v.FinishedAt
		v.FinishedAt = &t
	}
	return &v
}
