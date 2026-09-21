package main

import (
	"context"
	"controltower/agent/internal/config"
	"controltower/agent/internal/logarchive"
	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
	"errors"
	"strings"
	"testing"
	"time"
)

func validV2Control() (config.Config, ac.Status, ac.Response) {
	id := af.Identity{SiteID: "site", DatasetID: strings.Repeat("a", 32), SourceGenerationID: strings.Repeat("b", 32)}
	cfg := config.Config{AgentID: "agent", InstanceID: "instance", LogArchiveIdentity: id}
	st := ac.Status{SiteID: id.SiteID, AgentID: cfg.AgentID, Session: strings.Repeat("c", 32), Configured: true, Foundation: &af.FoundationStatus{Identity: id, ProtocolVersion: 2, FormatVersion: 2, Capabilities: []string{af.CapabilityFoundation, af.CapabilityAtomicWriter}}}
	c := ac.Default()
	c.Version = 2
	c.Running = true
	c.AgentID = cfg.AgentID
	c.InstanceID = cfg.InstanceID
	out := ac.Response{SiteID: id.SiteID, Config: c, Granted: true, LeaseSeconds: 120, WriterGrant: &af.WriterGrant{Identity: id, ProtocolVersion: 2, WriterEpoch: 1, Session: st.Session, ConfigVersion: 2, LeaseSeconds: 120}}
	return cfg, st, out
}

func TestArchiveWriterGrantBoundToIdentityAndOriginalDeadline(t *testing.T) {
	cfg, st, out := validV2Control()
	now := time.Now()
	_, remaining, ok := archiveWriterGrant(out, cfg, st.Session, now.Add(120*time.Second), now)
	if !ok || remaining != 90*time.Second {
		t.Fatal("valid grant rejected or extended", remaining)
	}
	for _, elapsed := range []time.Duration{85 * time.Second, 120 * time.Second, 300 * time.Second} {
		if _, _, ok := archiveWriterGrant(out, cfg, st.Session, now.Add(120*time.Second), now.Add(elapsed)); ok {
			t.Fatal("stale cached grant accepted", elapsed)
		}
	}
	for _, change := range []func(*ac.Response){
		func(o *ac.Response) { o.WriterGrant = nil }, func(o *ac.Response) { o.Granted = false }, func(o *ac.Response) { o.Config.Running = false }, func(o *ac.Response) { o.WriterGrant.DatasetID = strings.Repeat("d", 32) }, func(o *ac.Response) { o.WriterGrant.Session = strings.Repeat("d", 32) }, func(o *ac.Response) { o.WriterGrant.ConfigVersion++ }, func(o *ac.Response) { o.WriterGrant.WriterEpoch = 0 }, func(o *ac.Response) { o.Config.AgentID = "other" }, func(o *ac.Response) { o.LeaseSeconds = 90 },
	} {
		_, _, copy := validV2Control()
		change(&copy)
		if _, _, ok := archiveWriterGrant(copy, cfg, st.Session, now.Add(120*time.Second), now); ok {
			t.Fatal("mismatched authorization accepted")
		}
	}
}

func TestArchiveStatusPublicationOwnsMutableValues(t *testing.T) {
	_, st, _ := validV2Control()
	st.Days = []ac.Day{{Date: "2026-09-20"}}
	st.Reconciliation = &ac.Reconciliation{State: "running"}
	snapshot := cloneArchiveStatus(st)
	st.Foundation.WriterEpoch = 5
	st.Foundation.Capabilities[0] = "changed"
	st.Days[0].Date = "changed"
	st.Reconciliation.State = "changed"
	if snapshot.Foundation.WriterEpoch != 0 || snapshot.Foundation.Capabilities[0] != af.CapabilityFoundation || snapshot.Days[0].Date != "2026-09-20" || snapshot.Reconciliation.State != "running" {
		t.Fatal("poll snapshot shares mutable writer state")
	}
}

type atomicLoopWorker struct {
	acquires, passes, releases       int
	remaining                        time.Duration
	inspectErr, progressErr, passErr error
	acquireErr                       error
	result                           logarchive.BatchResult
	scans                            int
	scanResult                       af.BackfillStatus
	scanErr                          error
	budget                           af.ScanBudget
}

func (w *atomicLoopWorker) InspectFoundation(context.Context, af.Identity) (logarchive.FoundationInfo, error) {
	return logarchive.FoundationInfo{}, w.inspectErr
}
func (w *atomicLoopWorker) AcquireWriter(_ context.Context, _ af.WriterGrant, d time.Duration) error {
	w.acquires++
	w.remaining = d
	return w.acquireErr
}
func (w *atomicLoopWorker) ReleaseWriter(context.Context, af.WriterGrant) error {
	w.releases++
	return nil
}
func (w *atomicLoopWorker) PassV2(context.Context, af.WriterGrant) (logarchive.BatchResult, error) {
	w.passes++
	return w.result, w.passErr
}
func (w *atomicLoopWorker) ProgressV2(context.Context, af.Identity) (logarchive.BatchResult, error) {
	return w.result, w.progressErr
}
func (w *atomicLoopWorker) SetBatchSize(int)                           {}
func (w *atomicLoopWorker) WithDelay(time.Duration) *logarchive.Worker { return nil }
func (w *atomicLoopWorker) SetScanBudget(budget af.ScanBudget)         { w.budget = budget }
func (w *atomicLoopWorker) ScanDateV3(context.Context, af.WriterGrant, af.BackfillTask) (af.BackfillStatus, error) {
	w.scans++
	return w.scanResult, w.scanErr
}

func TestArchiveV2LoopPauseLossAndFailure(t *testing.T) {
	cfg, st, out := validV2Control()
	v := archiveV2Runner{}
	now := time.Now()
	w := &atomicLoopWorker{result: logarchive.BatchResult{BatchID: strings.Repeat("d", 32), AfterID: 42, Rows: 3, CommittedAt: now, CatalogRevision: 1}}
	v.step(context.Background(), w, cfg, &st, out, now.Add(120*time.Second), nil)
	if w.passes != 1 || w.remaining > 90*time.Second || st.LastID != 42 || st.Foundation.WriterEpoch != 1 || st.Foundation.ReceiptID != w.result.BatchID || st.State != "running" {
		t.Fatalf("valid batch did notpublish authoritative progress: %+v", st)
	}
	out.Config.Running = false
	v.step(context.Background(), w, cfg, &st, out, now.Add(120*time.Second), nil)
	if w.releases != 1 || w.passes != 1 || st.State != "paused" {
		t.Fatal("pause did notstopwriter")
	}
	out.Config.Running = true
	w.passErr = errors.New("simulated DB outage")
	v.step(context.Background(), w, cfg, &st, out, now.Add(120*time.Second), nil)
	if st.State != "error" || st.LastID != 42 || st.Foundation.ReceiptID != w.result.BatchID {
		t.Fatal("failedbatch advancedorclearedprogress")
	}
	v.step(context.Background(), w, cfg, &st, out, now.Add(120*time.Second), errors.New("controloffline"))
	if w.releases != 2 || st.State != "waiting" || w.passes != 2 {
		t.Fatal("controlfailure didn'tstopnewwork")
	}
}

func TestArchiveV2LoopCannotRunAfterFailedPreflight(t *testing.T) {
	cfg, st, out := validV2Control()
	w := &atomicLoopWorker{inspectErr: errors.New("identity mismatch")}
	v := archiveV2Runner{}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if w.acquires != 0 || w.passes != 0 || st.Configured || st.State != "error" || st.Foundation == nil {
		t.Fatal("preflightfailure lostidentityor allowedwriter")
	}
}

func TestArchiveWriterErrorsExplainRecoveryWithoutRawDetails(t *testing.T) {
	if !strings.Contains(archiveWriterError(logarchive.ErrWriterLegacyData), "explicit import") {
		t.Fatal("legacy adoption failure hidden")
	}
	if strings.Contains(archiveWriterError(errors.New("password=synthetic-secret")), "synthetic-secret") {
		t.Fatal("raw driver error exposed")
	}
}

func TestArchiveV2PreflightExplainsLegacyImportBlock(t *testing.T) {
	cfg, st, out := validV2Control()
	w := &atomicLoopWorker{progressErr: logarchive.ErrWriterLegacyData}
	v := archiveV2Runner{}
	v.step(context.Background(), w, cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if w.acquires != 0 || st.Configured || st.State != "error" || !strings.Contains(st.Error, "explicit import") {
		t.Fatal("legacy target must stop before acquiring a writer and explain the import requirement")
	}
}
