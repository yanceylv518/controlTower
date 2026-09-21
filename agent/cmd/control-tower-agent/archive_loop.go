package main

import (
	"context"
	"controltower/agent/internal/config"
	"controltower/agent/internal/controlpoll"
	"controltower/agent/internal/logarchive"
	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

type archiveControlState struct {
	sync.Mutex
	status   ac.Status
	response ac.Response
	expires  time.Time
	err      error
	accepted []ac.Day
}

func (s *archiveControlState) poll(ctx context.Context, cfg config.Config) {
	client := &http.Client{Timeout: 15 * time.Second, Transport: controlpoll.Transport(ctx)}
	for {
		s.Lock()
		st := cloneArchiveStatus(s.status)
		s.Unlock()
		started := time.Now()
		out, err := archivePoll(ctx, client, cfg, st)
		s.Lock()
		s.response = out
		s.err = err
		s.expires = started.Add(time.Duration(out.LeaseSeconds) * time.Second)
		if err == nil && out.StatusAccepted {
			s.accepted = append(s.accepted, st.Days...)
		}
		s.Unlock()
		timer := time.NewTimer(15 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func archiveMayRun(out ac.Response, expires, now time.Time, cfg config.Config) bool {
	if cfg.LogArchiveIdentity.DatasetID != "" {
		return false
	}
	return out.Granted && out.Config.Running && out.Config.Validate() && out.Config.AgentID == cfg.AgentID && out.Config.InstanceID == cfg.InstanceID && out.LeaseSeconds >= 60 && out.LeaseSeconds <= 120 && now.Add(45*time.Second).Before(expires)
}

func startManagedArchive(parent context.Context, cfg config.Config) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return
		}
		st := ac.Status{SupportsDailyCheck: true, AgentID: cfg.AgentID, Session: hex.EncodeToString(raw), State: "paused", Configured: cfg.LogArchiveEnabled && cfg.LogArchiveDSN != ""}
		foundationMode := cfg.LogArchiveIdentity.DatasetID != ""
		if foundationMode {
			st.SiteID = cfg.LogArchiveIdentity.SiteID
			st.SupportsDailyCheck = false
			st.Configured = false
			st.Foundation = &af.FoundationStatus{Identity: cfg.LogArchiveIdentity, ProtocolVersion: af.ProtocolVersion, FormatVersion: af.FormatVersion, Capabilities: []string{af.CapabilityFoundation, af.CapabilityAtomicWriter, af.CapabilityBackfill, af.CapabilityReconcile, af.CapabilitySeal, af.CapabilityWorkflow}}
		}
		if !st.Configured {
			st.State = "unconfigured"
		}
		var w *logarchive.Worker
		refresh := func() {
			if foundationMode {
				return
			}
			if w == nil {
				return
			}
			id, v, n, e := w.Progress()
			if e != nil {
				return
			}
			st.LastID = id
			st.VerifiedRows = n
			st.Days, _ = w.Days()
			if !v.IsZero() {
				st.VerifiedAt = &v
				st.LastSuccess = &v
			}
		}
		if st.Configured || foundationMode {
			w, _ = logarchive.Open(cfg.LogDSN, cfg.LogArchiveDSN, cfg.InstanceID, cfg.DataDir, cfg.LogArchiveBatchSize)
			refresh()
		}
		defer func() {
			if w != nil {
				w.Close()
			}
		}()
		shared := &archiveControlState{status: cloneArchiveStatus(st)}
		pollDone := make(chan struct{})
		go func() { defer close(pollDone); shared.poll(ctx, cfg) }()
		defer func() { cancel(); <-pollDone }()
		v2 := &archiveV2Runner{}
		defer func() {
			if w != nil {
				v2.release(context.Background(), w)
			}
		}()
		var next time.Time
		failures := 0
		var check *logarchive.Reconciler
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
			accepted := shared.accepted
			shared.accepted = nil
			shared.Unlock()
			if w != nil && len(accepted) > 0 {
				if e := w.AcknowledgeDays(accepted); e != nil {
					st.Error = "cannot acknowledge daily progress"
				}
				refresh()
			}
			c := out.Config
			if foundationMode {
				// V2 requires an identity-bound writer grant. An old Server's
				// legacy Granted flag can never enter either writer path.
				if w == nil {
					w, _ = logarchive.Open(cfg.LogDSN, cfg.LogArchiveDSN, cfg.InstanceID, cfg.DataDir, cfg.LogArchiveBatchSize)
				}
				if w == nil {
					v2.step(ctx, nil, cfg, &st, out, expires, controlErr)
				} else {
					v2.step(ctx, w, cfg, &st, out, expires, controlErr)
				}
				shared.Lock()
				shared.status = cloneArchiveStatus(st)
				shared.Unlock()
				continue
			}
			if out.SiteID != st.SiteID {
				st.SiteID = out.SiteID
				st.AppliedVersion = 0
			}
			if controlErr != nil {
				st.State = "waiting"
				st.Error = controlErr.Error()
			} else if !st.Configured {
				st.State = "unconfigured"
			} else if c.Version >= st.AppliedVersion && (!c.Running || c.Validate()) {
				st.AppliedVersion = c.Version
				if !c.Running {
					st.State = "paused"
					st.Error = ""
				} else if !archiveMayRun(out, expires, time.Now(), cfg) {
					st.State = "waiting"
				} else {
					st.State = "running"
					if time.Now().After(next) {
						var err error
						if w == nil {
							w, err = logarchive.Open(cfg.LogDSN, cfg.LogArchiveDSN, cfg.InstanceID, cfg.DataDir, c.BatchSize)
						}
						if err == nil {
							w.SetBatchSize(c.BatchSize)
							w.WithDelay(time.Duration(c.DelaySeconds) * time.Second)
							passCtx, stop := context.WithTimeout(ctx, 45*time.Second)
							if c.ReconcileID != "" {
								if check == nil || check.Result().ID != c.ReconcileID {
									check, err = w.NewReconciler(c.ReconcileID, c.ReconcileDate)
								}
								if err == nil {
									err = w.ReconcileStep(passCtx, check)
									result := check.Result()
									st.Reconciliation = &result
								}
							} else {
								check = nil
								if len(st.Days) < 4000 {
									st.LastBatchRows, err = w.Pass(passCtx)
									refresh()
								}
							}
							stop()
						}
						delay := time.Duration(c.IntervalSeconds) * time.Second
						if err != nil {
							failures++
							if failures > 4 {
								failures = 4
							}
							st.Error = err.Error()
							retry := time.Duration(30*(1<<failures)) * time.Second
							if retry > 5*time.Minute {
								retry = 5 * time.Minute
							}
							if delay < retry {
								delay = retry
							}
						} else {
							failures = 0
							st.Error = ""
						}
						next = time.Now().Add(delay)
					}
				}
			}
			shared.Lock()
			shared.status = cloneArchiveStatus(st)
			shared.Unlock()
		}
	}()
	return func() { cancel(); <-done }
}
