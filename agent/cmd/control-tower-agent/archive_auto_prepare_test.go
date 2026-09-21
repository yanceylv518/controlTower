package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"controltower/agent/internal/config"
	"controltower/agent/internal/logarchive"
	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
)

type autoPrepareFake struct {
	identity *af.Identity
	err      error
	calls    int
	seen     af.Identity
}

func (w *autoPrepareFake) FoundationIdentity(context.Context) (*af.Identity, error) {
	return w.identity, nil
}
func (w *autoPrepareFake) PrepareFoundation(_ context.Context, i af.Identity) (logarchive.FoundationInfo, error) {
	w.calls++
	w.seen = i
	return logarchive.FoundationInfo{Identity: i, FormatVersion: af.FormatVersion, SchemaFingerprint: strings.Repeat("a", 64), SourceFingerprint: strings.Repeat("b", 64)}, w.err
}
func autoFixture() (config.Config, ac.Status, ac.Response) {
	i := af.Identity{SiteID: "site", DatasetID: strings.Repeat("a", 32), SourceGenerationID: strings.Repeat("b", 32)}
	c := ac.Default()
	c.AgentID = "agent"
	c.InstanceID = "instance"
	c.Running = true
	c.Version = 1
	return config.Config{AgentID: "agent", InstanceID: "instance"}, ac.Status{AutoPrepare: true, Configured: true, AgentID: "agent", Session: strings.Repeat("c", 32), State: "waiting"}, ac.Response{SiteID: "site", Config: c, Prepare: &i, PrepareToken: strings.Repeat("d", 32), LeaseSeconds: 120}
}
func TestAutoPrepareDiscoveryMigrationBindingAndResume(t *testing.T) {
	cfg, st, out := autoFixture()
	w := &autoPrepareFake{}
	a := &archiveAutoRunner{}
	expires := time.Now().Add(120 * time.Second)
	if a.step(context.Background(), w, &cfg, &st, out, expires, nil) || w.calls != 0 || !st.PrepareDiscovered {
		t.Fatal("must publish discovery before migration")
	}
	if a.step(context.Background(), w, &cfg, &st, out, expires, nil) || w.calls != 1 || st.Prepared == nil || !st.Validate() {
		t.Fatalf("prepare: %+v", st)
	}
	a.step(context.Background(), w, &cfg, &st, out, expires, nil)
	if w.calls != 1 {
		t.Fatal("re-ran DDL while awaiting registration")
	}
	out.PrepareToken = strings.Repeat("e", 32)
	a.step(context.Background(), w, &cfg, &st, out, expires, nil)
	if w.calls != 2 || st.PreparedToken != out.PrepareToken {
		t.Fatal("did not recheck after expired authorization")
	}
	out.PreparedAccepted = true
	if !a.step(context.Background(), w, &cfg, &st, out, expires, nil) || !cfg.LogArchiveIdentity.Equal(*out.Prepare) || st.Foundation == nil || !st.Validate() {
		t.Fatalf("bind: %+v", st)
	}
	// After a process restart, discovery must reuse target metadata.
	cfg2, st2, out2 := autoFixture()
	a2 := &archiveAutoRunner{}
	w.identity = out.Prepare
	a2.step(context.Background(), w, &cfg2, &st2, out2, expires, nil)
	if st2.PrepareIdentity == nil || !st2.PrepareIdentity.Equal(cfg.LogArchiveIdentity) {
		t.Fatal("restart lost identity")
	}
}
func TestAutoPrepareFailureRetriesSameIdentity(t *testing.T) {
	cfg, st, out := autoFixture()
	w := &autoPrepareFake{err: errors.New("secret DSN")}
	a := &archiveAutoRunner{discovered: true}
	expires := time.Now().Add(120 * time.Second)
	a.step(context.Background(), w, &cfg, &st, out, expires, nil)
	if st.State != "error" || strings.Contains(st.Error, "secret") || st.Prepared != nil {
		t.Fatal("failed DDL reported success or leaked connection")
	}
	a.step(context.Background(), w, &cfg, &st, out, expires, nil)
	if w.calls != 1 {
		t.Fatal("retry spun")
	}
	a.next = time.Time{}
	w.err = nil
	a.step(context.Background(), w, &cfg, &st, out, expires, nil)
	if w.calls != 2 || !w.seen.Equal(*out.Prepare) || st.Prepared == nil {
		t.Fatal("retry changed identity")
	}
}
func TestAutoPrepareNeverUsesLegacyOrExpiredGrant(t *testing.T) {
	for _, kind := range []string{"legacy", "expired", "paused", "other-agent", "control-error", "identity"} {
		t.Run(kind, func(t *testing.T) {
			cfg, st, out := autoFixture()
			w := &autoPrepareFake{}
			a := &archiveAutoRunner{discovered: true}
			expires := time.Now().Add(120 * time.Second)
			var err error
			switch kind {
			case "legacy":
				out.Prepare = nil
				out.Granted = true
			case "expired":
				expires = time.Now()
			case "paused":
				out.Config.Running = false
			case "other-agent":
				out.Config.AgentID = "other"
			case "control-error":
				err = errors.New("offline")
			case "identity":
				i := *out.Prepare
				i.SiteID = "other"
				st.PrepareIdentity = &i
			}
			a.step(context.Background(), w, &cfg, &st, out, expires, err)
			if w.calls != 0 {
				t.Fatal("unauthorized DDL")
			}
		})
	}
}
func TestAutoPrepareStatusClone(t *testing.T) {
	_, st, out := autoFixture()
	st.PrepareIdentity = out.Prepare
	st.Prepared = &af.Registration{Identity: *out.Prepare}
	copy := cloneArchiveStatus(st)
	copy.PrepareIdentity.SiteID = "other"
	copy.Prepared.SiteID = "other"
	if st.PrepareIdentity.SiteID != "site" || st.Prepared.SiteID != "site" {
		t.Fatal("poll status shares mutable identity")
	}
}

func TestAutoPrepareConnectionFailureAcknowledgesControl(t *testing.T) {
	cfg, st, out := autoFixture()
	a := &archiveAutoRunner{}
	a.step(context.Background(), nil, &cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if st.AppliedVersion != out.Config.Version || st.SiteID != out.SiteID || st.Error == "" {
		t.Fatalf("connection failure hidden behind pending configuration: %+v", st)
	}
}

func TestAutoPrepareServerRejectionIsVisibleWithoutDDL(t *testing.T) {
	cfg, st, out := autoFixture()
	out.PrepareError = "archive_prepare_tasks_pending"
	a := &archiveAutoRunner{discovered: true}
	w := &autoPrepareFake{}
	a.step(context.Background(), w, &cfg, &st, out, time.Now().Add(120*time.Second), nil)
	if w.calls != 0 || st.Error != out.PrepareError || st.PreparePhase != "failed" || st.AppliedVersion != out.Config.Version {
		t.Fatalf("rejection hidden or bypassed: %+v", st)
	}
}
