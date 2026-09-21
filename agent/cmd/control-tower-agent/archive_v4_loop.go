package main

import (
	"context"
	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
	"errors"
	"time"
)

type verifiedArchiveWorker interface {
	ReconcileDateV4(context.Context, af.WriterGrant, af.ReconcileTask) (af.ReconcileStatus, error)
	SealDaysV4(context.Context, af.WriterGrant, af.SealTask) (af.SealStatus, error)
}

func archiveVerificationTask(out ac.Response, st *ac.Status, g af.WriterGrant) (af.CoveragePolicy, bool) {
	if st.Foundation == nil {
		return af.CoveragePolicy{}, false
	}
	// A dispatch containing multiple task types is invalid, not a priority hint.
	n := 0
	if out.BackfillTask != nil {
		n++
	}
	if out.ReconcileTask != nil {
		n++
	}
	if out.SealTask != nil {
		n++
	}
	if n != 1 {
		return af.CoveragePolicy{}, false
	}
	if t := out.ReconcileTask; t != nil && st.Foundation.SupportsReconcile() && t.Validate() == nil && t.Identity.Equal(g.Identity) {
		if p := st.Reconcile; p != nil && p.TaskID == t.TaskID && p.Attempt == t.Attempt && p.WriterEpoch == g.WriterEpoch && (p.State == "matched" || p.State == "mismatched" || p.State == "blocked" || p.State == "retry_wait" && time.Now().Unix() < p.RetryAfterUnix) {
			return af.CoveragePolicy{}, false
		}
		return t.Policy, true
	}
	if t := out.SealTask; t != nil && st.Foundation.SupportsSeal() && t.Validate() == nil && t.Identity.Equal(g.Identity) {
		if p := st.Seal; p != nil && p.TaskID == t.TaskID && p.Attempt == t.Attempt && p.WriterEpoch == g.WriterEpoch && (p.State == "succeeded" || p.State == "blocked" || p.State == "retry_wait" && time.Now().Unix() < p.RetryAfterUnix) {
			return af.CoveragePolicy{}, false
		}
		return t.Policy, true
	}
	return af.CoveragePolicy{}, false
}

func runArchiveVerification(ctx context.Context, worker atomicArchiveWorker, st *ac.Status, out ac.Response, g af.WriterGrant) error {
	w, ok := worker.(verifiedArchiveWorker)
	if !ok {
		return errors.New("archive verification worker unavailable")
	}
	if t := out.ReconcileTask; t != nil {
		result, err := w.ReconcileDateV4(ctx, g, *t)
		if err != nil && result.Validate() != nil {
			result = af.ReconcileStatus{TaskID: t.TaskID, Date: t.Date, Attempt: t.Attempt, WriterEpoch: g.WriterEpoch, State: "running", Phase: "source_first", Method: "stable_window_paged"}
			if old := st.Reconcile; old != nil && old.TaskID == t.TaskID && old.Attempt == t.Attempt {
				result = *old
				result.WriterEpoch = g.WriterEpoch
			}
		}
		if err != nil && (result.State == "running" || result.State == "retry_wait") {
			result.State = "retry_wait"
			result.ErrorCode = "reconcile_failed"
			result.RetryAfterUnix = time.Now().Add(30 * time.Second).Unix()
		}
		if result.Validate() == nil && result.TaskID == t.TaskID && result.Date == t.Date && result.Attempt == t.Attempt && result.WriterEpoch == g.WriterEpoch {
			st.Reconcile = &result
			if result.CatalogRevision > st.Foundation.CatalogRevision {
				st.Foundation.CatalogRevision = result.CatalogRevision
			}
		} else if err == nil {
			err = errors.New("invalid archive verification result")
		}
		return err
	}
	if t := out.SealTask; t != nil {
		result, err := w.SealDaysV4(ctx, g, *t)
		if err != nil && result.Validate() != nil {
			result = af.SealStatus{TaskID: t.TaskID, Attempt: t.Attempt, WriterEpoch: g.WriterEpoch, State: "running"}
			if old := st.Seal; old != nil && old.TaskID == t.TaskID && old.Attempt == t.Attempt {
				result = *old
				result.WriterEpoch = g.WriterEpoch
			}
		}
		if err != nil && (result.State == "running" || result.State == "retry_wait") {
			result.State = "retry_wait"
			result.ErrorCode = "target_unavailable"
			result.RetryAfterUnix = time.Now().Add(30 * time.Second).Unix()
		}
		valid := result.Validate() == nil && result.TaskID == t.TaskID && result.Attempt == t.Attempt && result.WriterEpoch == g.WriterEpoch
		if result.State == "succeeded" {
			if len(result.Versions) != len(t.Dates) {
				valid = false
			} else {
				for i, d := range result.Versions {
					if d.Date != t.Dates[i] {
						valid = false
					}
				}
			}
		}
		if valid {
			st.Seal = &result
			if result.CatalogRevision > st.Foundation.CatalogRevision {
				st.Foundation.CatalogRevision = result.CatalogRevision
			}
		} else if err == nil {
			err = errors.New("invalid archive seal result")
		}
		return err
	}
	return errors.New("archive verification dispatch missing")
}
