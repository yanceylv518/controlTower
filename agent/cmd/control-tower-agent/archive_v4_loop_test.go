package main

import (
	"context"
	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
	"errors"
	"strings"
	"testing"
	"time"
)

type verifiedLoopWorker struct {
	atomicLoopWorker
	checks, seals int
	reconcile     af.ReconcileStatus
	seal          af.SealStatus
	operationErr  error
}

func (w *verifiedLoopWorker) ReconcileDateV4(context.Context, af.WriterGrant, af.ReconcileTask) (af.ReconcileStatus, error) {
	w.checks++
	return w.reconcile, w.operationErr
}
func (w *verifiedLoopWorker) SealDaysV4(context.Context, af.WriterGrant, af.SealTask) (af.SealStatus, error) {
	w.seals++
	return w.seal, w.operationErr
}
func readyVerification(st *ac.Status, out *ac.Response) af.ReconcileStatus {
	st.Foundation.Capabilities = append(st.Foundation.Capabilities, af.CapabilityBackfill, af.CapabilityReconcile, af.CapabilitySeal)
	date := "2026-09-01"
	_, end, _ := af.DateBounds(date)
	out.ReconcileTask = &af.ReconcileTask{Identity: st.Foundation.Identity, TaskID: strings.Repeat("e", 32), Date: date, Attempt: 1, Policy: af.DefaultCoveragePolicy(), Assurance: af.VerificationAssurance{StableBeforeUnix: end, ValidUntilUnix: time.Now().Add(time.Hour).Unix(), Evidence: "synthetic immutable history assertion"}}
	return af.ReconcileStatus{TaskID: out.ReconcileTask.TaskID, RunID: strings.Repeat("f", 32), Date: date, Attempt: 1, WriterEpoch: out.WriterGrant.WriterEpoch, State: "running", Phase: "source_first", Method: "stable_window_paged", ProgressVersion: 1}
}
func TestArchiveV4VerificationSharesIncrementalBudget(t *testing.T) {
	cfg, st, out := validV2Control()
	w := &verifiedLoopWorker{reconcile: readyVerification(&st, &out)}
	w.result.More = true
	v := archiveV2Runner{}
	for n := 0; n < 8; n++ {
		v.nextPass = time.Time{}
		v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	}
	if w.checks != 2 || w.passes != 6 || st.Reconcile == nil || !st.Validate() {
		t.Fatalf("verification fairness/status: checks=%d passes=%d status=%+v", w.checks, w.passes, st)
	}
}
func TestArchiveV4TransientFailureRetainsEvidenceAndYieldsQueue(t *testing.T) {
	cfg, st, out := validV2Control()
	report := readyVerification(&st, &out)
	w := &verifiedLoopWorker{reconcile: report, operationErr: errors.New("database transport error")}
	v := archiveV2Runner{incrementalTurns: 3}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if st.Reconcile == nil || st.Reconcile.State != "retry_wait" || st.Reconcile.ProgressVersion != report.ProgressVersion || st.Reconcile.RunID != report.RunID || st.Reconcile.RetryAfterUnix <= time.Now().Unix() || !st.Validate() {
		t.Fatalf("invalid operational retry: %+v", st.Reconcile)
	}
	v.nextPass = time.Time{}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if w.passes != 1 || w.checks != 1 {
		t.Fatal("transient verification starved incremental")
	}
}
func TestArchiveV4RejectsPartialSealPublicationAndClonesPointers(t *testing.T) {
	_, st, out := validV2Control()
	readyVerification(&st, &out)
	out.ReconcileTask = nil
	task := af.SealTask{Identity: st.Foundation.Identity, TaskID: strings.Repeat("e", 32), Dates: []string{"2026-09-01", "2026-09-02"}, Attempt: 1, Policy: af.DefaultCoveragePolicy()}
	out.SealTask = &task
	result := af.SealStatus{TaskID: task.TaskID, BuildID: strings.Repeat("f", 32), Attempt: 1, WriterEpoch: 1, ProgressVersion: 2, CatalogRevision: 4, State: "succeeded", Versions: []af.SealedDay{{Date: task.Dates[0], VersionID: strings.Repeat("d", 32), VersionNo: 1, ManifestHash: strings.Repeat("a", 64)}}}
	w := &verifiedLoopWorker{seal: result}
	if runArchiveVerification(context.Background(), w, &st, out, *out.WriterGrant) == nil || st.Seal != nil {
		t.Fatal("partially published cohort accepted")
	}
	st.Seal = &result
	snapshot := cloneArchiveStatus(st)
	st.Seal.Versions[0].Date = "changed"
	if snapshot.Seal.Versions[0].Date == "changed" {
		t.Fatal("seal status aliases mutable version list")
	}
}
func TestArchiveV4NewEpochDropsOldTaskReports(t *testing.T) {
	cfg, st, out := validV2Control()
	r := readyVerification(&st, &out)
	st.Reconcile = &r
	st.Foundation.WriterEpoch = 1
	out.WriterGrant.WriterEpoch = 2
	out.ReconcileTask = nil
	w := &verifiedLoopWorker{}
	v := archiveV2Runner{}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if st.Reconcile != nil || st.Foundation.WriterEpoch != 2 || !st.Validate() {
		t.Fatal("stale task report poisoned new writer epoch")
	}
}
