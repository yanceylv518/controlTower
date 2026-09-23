package main

import (
	"bytes"
	"context"
	engine "controltower/agent/internal/archivejob"
	"controltower/agent/internal/config"
	ac "controltower/internal/archivecontrol"
	aj "controltower/internal/archivejob"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

func archiveJobsPoll(ctx context.Context, client *http.Client, cfg config.Config, st ac.Status) (ac.Response, error) {
	var out ac.Response
	b, _ := json.Marshal(st)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(cfg.ServerURL, "/")+"/api/agent/log-archive-jobs/poll", bytes.NewReader(b))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AgentToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return out, errors.New("archive_control_unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return out, errors.New("new_archive_protocol_required")
	}
	if json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&out) != nil || out.Config.Tasks == nil {
		return out, errors.New("new_archive_protocol_required")
	}
	return out, nil
}

type archiveJobsReporter interface {
	Refresh(context.Context) (aj.Status, error)
}

// Pausing tasks stops source collection and history, not archive count reports.
// StatusAccepted identifies the current lease owner even when Granted is false.
func refreshArchiveJobs(ctx context.Context, worker archiveJobsReporter, cfg config.Config, out ac.Response, started time.Time, st *ac.Status) {
	if worker == nil || !st.Configured || !out.StatusAccepted || out.Config.AgentID != cfg.AgentID || out.Config.InstanceID != cfg.InstanceID || !out.Config.Validate() {
		return
	}
	remaining := time.Duration(out.LeaseSeconds)*time.Second - time.Since(started) - 5*time.Second
	if remaining > 10*time.Second {
		remaining = 10 * time.Second
	}
	if remaining <= 0 {
		return
	}
	reportCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	snapshot, err := worker.Refresh(reportCtx)
	if err != nil && snapshot.FirstDate == "" && st.Engine != nil {
		// A failed read must not erase the last known archive position.
		st.Engine.CountsError = snapshot.CountsError
		st.Engine.CountsDate = snapshot.CountsDate
		return
	}
	if st.State == "error" && st.Engine != nil {
		if snapshot.Collection.Error == "" {
			snapshot.Collection.Error = st.Engine.Collection.Error
		}
		if snapshot.History.Error == "" {
			snapshot.History.Error = st.Engine.History.Error
		}
	}
	st.Engine = &snapshot
}

func startArchiveJobs(parent context.Context, cfg config.Config) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return
		}
		st := ac.Status{AgentID: cfg.AgentID, Session: hex.EncodeToString(raw[:]), State: "unconfigured", Engine: &aj.Status{Protocol: aj.Protocol, Days: []aj.Day{}}}
		var worker *engine.Engine
		defer func() {
			if worker != nil {
				worker.Close()
			}
		}()
		client := &http.Client{Timeout: 10 * time.Second}
		for ctx.Err() == nil {
			if worker == nil && cfg.LogArchiveEnabled {
				var err error
				worker, err = engine.Open(cfg.LogDSN, cfg.LogArchiveDSN)
				st.Configured = err == nil
				if err != nil {
					st.Error = "archive_connection_config_invalid"
				}
			}
			started := time.Now()
			out, err := archiveJobsPoll(ctx, client, cfg, st)
			wait := 2 * time.Second
			if err != nil {
				st.State = "waiting"
				st.Error = err.Error()
				wait = 15 * time.Second
			} else {
				st.SiteID = out.SiteID
				st.AppliedVersion = out.Config.Version
				var bindErr error
				if worker != nil {
					bindErr = worker.BindSite(out.SiteID)
					if bindErr != nil {
						st.State = "error"
						st.Error = bindErr.Error()
						out.Granted = false
						out.StatusAccepted = false
					}
				}
				if bindErr != nil {
					wait = 15 * time.Second
				} else if !out.Config.Running {
					st.State = "paused"
					st.Error = ""
					wait = 15 * time.Second
				} else if worker != nil && out.Granted && out.Config.AgentID == cfg.AgentID && out.Config.InstanceID == cfg.InstanceID && out.Config.Validate() {
					remaining := 30*time.Second - time.Since(started) - 5*time.Second
					if remaining > 10*time.Second {
						remaining = 10 * time.Second
					}
					if remaining > 0 {
						stepCtx, stop := context.WithTimeout(ctx, remaining)
						snapshot, stepErr := worker.Step(stepCtx, *out.Config.Tasks, out.Config.BatchSize, out.Config.DelaySeconds, out.Config.HistoryImmutable)
						stop()
						st.Engine = &snapshot
						st.State = "running"
						st.Error = ""
						if stepErr != nil {
							st.State = "error"
							st.Error = "archive_batch_failed_see_task_error"
						}
					}
					wait = time.Duration(out.Config.IntervalSeconds) * time.Second
				} else {
					st.State = "waiting"
				}
				if worker != nil {
					refreshArchiveJobs(ctx, worker, cfg, out, started, &st)
				}
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
