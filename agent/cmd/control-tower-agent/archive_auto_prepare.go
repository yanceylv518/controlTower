package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"time"

	"controltower/agent/internal/config"
	"controltower/agent/internal/logarchive"
	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
)

type automaticArchiveWorker interface {
	FoundationIdentity(context.Context) (*af.Identity, error)
	PrepareFoundation(context.Context, af.Identity) (logarchive.FoundationInfo, error)
}

type archiveAutoRunner struct {
	next       time.Time
	discovered bool
}

func (a *archiveAutoRunner) step(ctx context.Context, w automaticArchiveWorker, cfg *config.Config, st *ac.Status, out ac.Response, expires time.Time, controlErr error) bool {
	if controlErr != nil {
		st.State = "waiting"
		st.Error = "archive control unavailable"
		return false
	}
	if out.SiteID == "" {
		return false
	}
	st.SiteID = out.SiteID
	st.AppliedVersion = out.Config.Version
	if out.PrepareError != "" {
		st.State = "error"
		st.PreparePhase = "failed"
		switch out.PrepareError {
		case "archive_prepare_identity_mismatch", "archive_prepare_registration_conflict", "archive_prepare_tasks_pending":
			st.Error = out.PrepareError
		default:
			st.Error = "archive_prepare_control_rejected"
		}
		return false
	}
	if w == nil {
		st.State = "unconfigured"
		st.PreparePhase = "failed"
		st.Error = "archive_prepare_connection_configuration_invalid"
		return false
	}
	if out.PreparedAccepted && st.Prepared != nil && st.Prepared.SiteID == out.SiteID {
		cfg.LogArchiveIdentity = st.Prepared.Identity
		st.Foundation = &af.FoundationStatus{Identity: cfg.LogArchiveIdentity, ProtocolVersion: af.ProtocolVersion, FormatVersion: af.FormatVersion, Capabilities: []string{af.CapabilityFoundation, af.CapabilityAtomicWriter, af.CapabilityBackfill, af.CapabilityReconcile, af.CapabilitySeal, af.CapabilityWorkflow}}
		st.Prepared = nil
		st.PreparedToken = ""
		st.PrepareIdentity = nil
		st.PreparePhase = ""
		st.State = "waiting"
		st.Error = ""
		return true
	}
	if !out.Config.Running {
		st.State = "paused"
		st.Error = ""
		st.PreparePhase = ""
		return false
	}
	if out.Config.AgentID != cfg.AgentID || out.Config.InstanceID != cfg.InstanceID {
		st.State = "waiting"
		return false
	}
	if time.Now().Before(a.next) {
		return false
	}
	if !a.discovered {
		st.PreparePhase = "checking"
		checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		identity, err := w.FoundationIdentity(checkCtx)
		cancel()
		if err != nil {
			a.fail(st, err)
			return false
		}
		if cfg.LogArchiveIdentity != (af.Identity{}) {
			if cfg.LogArchiveIdentity.SiteID != out.SiteID || (identity != nil && !identity.Equal(cfg.LogArchiveIdentity)) {
				a.fail(st, logarchive.ErrFoundationIdentity)
				return false
			}
			identity = &cfg.LogArchiveIdentity
		}
		if identity != nil && identity.SiteID != out.SiteID {
			a.fail(st, logarchive.ErrFoundationIdentity)
			return false
		}
		st.PrepareIdentity = identity
		st.PrepareDiscovered = true
		a.discovered = true
		// Always publish discovery before accepting a preparation identity.
		return false
	}
	if st.Prepared != nil {
		if out.Prepare != nil && out.PrepareToken != "" && out.PrepareToken != st.PreparedToken {
			// An expired/replaced authorization requires a fresh target check,
			// not replaying evidence from a previous preparation attempt.
			st.Prepared = nil
			st.PreparedToken = ""
		} else {
			st.State = "waiting"
			st.Error = ""
			st.PreparePhase = "registering"
			return false
		}
	}
	if out.Prepare == nil {
		st.State = "waiting"
		st.Error = ""
		st.PreparePhase = "waiting_authorization"
		return false
	}
	if out.Prepare.Validate() != nil || out.Prepare.SiteID != out.SiteID || (st.PrepareIdentity != nil && !st.PrepareIdentity.Equal(*out.Prepare)) {
		a.fail(st, logarchive.ErrFoundationIdentity)
		return false
	}
	if _, err := af.IDBytes(out.PrepareToken); err != nil {
		return false
	}
	remaining := time.Until(expires) - 30*time.Second
	if out.LeaseSeconds < 60 || out.LeaseSeconds > 120 || remaining < 5*time.Second || remaining > 90*time.Second {
		return false
	}
	st.State = "waiting"
	st.Error = ""
	st.PreparePhase = "migrating"
	log.Printf("log archive: preparing schema dataset=%s", out.Prepare.DatasetID)
	prepareCtx, cancel := context.WithTimeout(ctx, remaining)
	info, err := w.PrepareFoundation(prepareCtx, *out.Prepare)
	cancel()
	if err != nil {
		a.fail(st, err)
		return false
	}
	st.PrepareIdentity = &info.Identity
	st.Prepared = &af.Registration{Identity: info.Identity, StorageRef: "agent-" + info.Identity.DatasetID, ArchiveFormatVersion: info.FormatVersion, SchemaFingerprint: info.SchemaFingerprint, SourceFingerprint: info.SourceFingerprint}
	st.PreparedToken = out.PrepareToken
	st.State = "waiting"
	st.Error = ""
	st.PreparePhase = "registering"
	log.Printf("log archive: schema prepared dataset=%s; waiting for binding", info.Identity.DatasetID)
	return false
}

func (a *archiveAutoRunner) fail(st *ac.Status, err error) {
	st.State = "error"
	st.PreparePhase = "failed"
	switch {
	case errors.Is(err, logarchive.ErrFoundationIdentity):
		st.Error = "archive_prepare_identity_mismatch"
	case errors.Is(err, logarchive.ErrFoundationSchema):
		st.Error = "archive_prepare_schema_mismatch"
	case errors.Is(err, logarchive.ErrFoundationVersion):
		st.Error = "archive_prepare_version_unsupported"
	default:
		st.Error = "archive_prepare_failed_check_connection_permissions_or_writer_lease"
	}
	log.Printf("log archive: %s; retry in 30s", st.Error)
	a.next = time.Now().Add(30 * time.Second)
}

func startAutomaticManagedArchive(parent context.Context, cfg config.Config) func() {
	return startAutomaticManagedArchiveWithPollInterval(parent, cfg, 15*time.Second)
}

func startAutomaticManagedArchiveWithPollInterval(parent context.Context, cfg config.Config, interval time.Duration) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return
		}
		st := ac.Status{AutoPrepare: true, AgentID: cfg.AgentID, Session: hex.EncodeToString(raw), State: "paused", Configured: cfg.LogArchiveEnabled && cfg.LogArchiveDSN != ""}
		if !st.Configured {
			st.State = "unconfigured"
		}
		shared := &archiveControlState{status: cloneArchiveStatus(st), pollInterval: interval}
		pollDone := make(chan struct{})
		go func() { defer close(pollDone); shared.poll(ctx, cfg) }()
		defer func() { cancel(); <-pollDone }()
		var w *logarchive.Worker
		var nextOpen time.Time
		v2 := &archiveV2Runner{}
		auto := &archiveAutoRunner{}
		ready := false
		defer func() {
			if w != nil {
				v2.release(context.Background(), w)
				w.Close()
			}
		}()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			shared.Lock()
			out, expires, controlErr := shared.response, shared.expires, shared.err
			shared.Unlock()
			if w == nil && cfg.LogArchiveEnabled && cfg.LogArchiveDSN != "" && !time.Now().Before(nextOpen) {
				w, _ = logarchive.Open(cfg.LogDSN, cfg.LogArchiveDSN, cfg.InstanceID, cfg.DataDir, cfg.LogArchiveBatchSize)
				nextOpen = time.Now().Add(30 * time.Second)
			}
			if ready {
				v2.step(ctx, w, cfg, &st, out, expires, controlErr)
			} else {
				// Publish the stage before blocking in DDL so the independent poll
				// goroutine keeps the UI and preparation lease alive.
				if out.Prepare != nil && st.Prepared == nil && !time.Now().Before(auto.next) {
					st.Error = ""
					st.PreparePhase = "migrating"
					st.State = "waiting"
				}
				shared.Lock()
				shared.status = cloneArchiveStatus(st)
				shared.Unlock()
				if w == nil {
					ready = auto.step(ctx, nil, &cfg, &st, out, expires, controlErr)
				} else {
					ready = auto.step(ctx, w, &cfg, &st, out, expires, controlErr)
				}
			}
			shared.Lock()
			shared.status = cloneArchiveStatus(st)
			shared.Unlock()
		}
	}()
	return func() { cancel(); <-done }
}
