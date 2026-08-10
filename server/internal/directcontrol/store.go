// Package directcontrol lets the server write channel weights, run recovery
// probes and verify control access against new-api over HTTP, instead of
// queueing channel commands for an agent. The direct path activates per site
// when a control config (API URL + admin token) is stored; sites without one
// keep the agent command queue unchanged.
package directcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/mysqlstore"
	"controltower/server/internal/secrets"
	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
)

// Controller is the slice of the new-api admin client the direct path uses.
type Controller interface {
	Update(context.Context, channelcontrol.UpdateRequest) (channelcontrol.Result, error)
	Probe(ctx context.Context, channelID int64, model string) (channelcontrol.ProbeResult, error)
	Check(context.Context) error
}

type Factory func(baseURL, accessToken string, adminUserID int64) Controller

func DefaultFactory(baseURL, accessToken string, adminUserID int64) Controller {
	return channelcontrol.New(baseURL, accessToken, adminUserID, nil)
}

const writeTimeout = 15 * time.Second

// probeRoundTimeout stays below the engine's 10-minute lost-probe fallback so
// a finished round always reports before the state machine gives up on it.
const probeRoundTimeout = 8 * time.Minute

// Store wraps the MySQL store; every method not overridden here behaves
// exactly as before, so the engine and the dashboard handlers stay unchanged.
type Store struct {
	mysqlstore.Store
	secretKey string
	factory   Factory
	sleep     func(context.Context, time.Duration)
}

// The engine discovers continuous-dispatch persistence via a runtime type
// assertion that degrades to "tuning silently off" when unsatisfied; this
// pins the wrapper to that contract at compile time instead.
var _ tuning.ContinuousStore = Store{}

func Wrap(inner mysqlstore.Store, secretKey string) Store {
	return Store{Store: inner, secretKey: secretKey, factory: DefaultFactory, sleep: sleepContext}
}

// WithFactory returns a copy using a custom controller factory (tests).
func (s Store) WithFactory(factory Factory, sleep func(context.Context, time.Duration)) Store {
	s.factory = factory
	if sleep != nil {
		s.sleep = sleep
	}
	return s
}

func sleepContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// controllerForSite resolves the per-site direct controller. ok=false means
// the site has no direct config and callers must fall back to the queue.
func (s Store) controllerForSite(siteID string) (Controller, bool, error) {
	cfg, err := s.Store.ControlConfigForSite(siteID)
	if err != nil {
		return nil, false, err
	}
	if cfg.APIURL == "" || cfg.EncryptedToken == "" {
		return nil, false, nil
	}
	token, err := secrets.Decrypt(s.secretKey, cfg.EncryptedToken)
	if err != nil {
		return nil, false, fmt.Errorf("decrypt control token: %w", err)
	}
	return s.factory(cfg.APIURL, token, cfg.AdminUserID), true, nil
}

// CreateContinuousWeightChange writes the weight to new-api synchronously on
// direct sites, then records the same paper trail the command path leaves.
// The recommendation's InstanceID carries the site id (engine convention).
func (s Store) CreateContinuousWeightChange(v tuning.Recommendation, actor string, now time.Time) (string, error) {
	controller, direct, err := s.controllerForSite(v.InstanceID)
	if err != nil {
		return "", err
	}
	if !direct {
		return s.Store.CreateContinuousWeightChange(v, actor, now)
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if err := executeWeightUpdate(ctx, controller, v); err != nil {
		return "", fmt.Errorf("direct weight write: %w", err)
	}
	return s.Store.RecordDirectWeightChange(v, actor, now)
}

func executeWeightUpdate(ctx context.Context, controller Controller, v tuning.Recommendation) error {
	if v.ProposedWeight < 0 {
		return fmt.Errorf("proposed weight must not be negative")
	}
	weight := uint(v.ProposedWeight)
	request := channelcontrol.UpdateRequest{ChannelID: v.ChannelID, Weight: &weight}
	if v.ProposedPriority != nil && (v.Rule == "circuit_opened" || v.Rule == "circuit_recovered") {
		request.Priority = v.ProposedPriority
	}
	_, err := controller.Update(ctx, request)
	return err
}

// CreateContinuousProbe runs the probe round from the server on direct sites.
// The command row is recorded as executing and completed when the round ends,
// feeding RecordContinuousProbeResult exactly like an agent-reported round.
func (s Store) CreateContinuousProbe(v tuning.Recommendation, model string, count, interval int, now time.Time) (string, error) {
	controller, direct, err := s.controllerForSite(v.InstanceID)
	if err != nil {
		return "", err
	}
	if !direct {
		return s.Store.CreateContinuousProbe(v, model, count, interval, now)
	}
	commandID, err := s.Store.CreateContinuousProbeExecuting(v, model, count, interval, now)
	if err != nil {
		return "", err
	}
	go s.runProbeRound(controller, v.InstanceID, v.ChannelID, commandID, model, count, interval)
	return commandID, nil
}

func (s Store) runProbeRound(controller Controller, siteID string, channelID int64, commandID, model string, count, interval int) {
	ctx, cancel := context.WithTimeout(context.Background(), probeRoundTimeout)
	defer cancel()
	// Ordering guard: the engine persists probe_command_id into the state row
	// only after CreateContinuousProbe returns. A round that finishes before
	// that (instant connection failures, probe_count=1) would report into an
	// UPDATE matching no row and be lost until the 10-minute fallback. Probes
	// therefore start only once the persisted marker references this round.
	if !waitForProbeMarker(ctx, s.Store, siteID, channelID, commandID, s.sleep) {
		_, _, _ = s.Store.CompleteChannelCommand(commandID, "failed", "probe marker was never persisted", time.Now().UTC())
		return
	}
	attempts, successes, durationSum, lastError := executeProbeRound(ctx, controller, channelID, model, count, interval, s.sleep)
	now := time.Now().UTC()
	status := "succeeded"
	if attempts == 0 {
		status = "failed"
	}
	if _, _, err := s.Store.CompleteChannelCommand(commandID, status, lastError, now); err != nil {
		return
	}
	summary, _ := json.Marshal(map[string]any{"result": map[string]any{"status": status, "error": lastError, "attempts": attempts, "successes": successes, "duration_seconds": durationSum, "direct": true}})
	_ = s.Store.InsertOperationAudit(storage.OperationAudit{ID: commandID, InstanceID: siteID, OperationType: "channel.probe", TargetType: "channel", TargetID: fmt.Sprint(channelID), ActorID: "system:auto", AfterSummary: string(summary), Status: status, CreatedAt: now})
	_ = s.Store.RecordContinuousProbeResult(siteID, channelID, commandID, attempts, successes, durationSum, now)
}

type continuousStateLister interface {
	ListContinuousStates(string) ([]tuning.ContinuousState, error)
}

const probeMarkerWait = 30 * time.Second
const probeMarkerPoll = 200 * time.Millisecond

func waitForProbeMarker(ctx context.Context, lister continuousStateLister, siteID string, channelID int64, commandID string, sleep func(context.Context, time.Duration)) bool {
	deadline := time.Now().Add(probeMarkerWait)
	for {
		states, err := lister.ListContinuousStates(siteID)
		if err == nil {
			for _, state := range states {
				if state.ChannelID == channelID && state.ProbeCommandID != nil && *state.ProbeCommandID == commandID {
					return true
				}
			}
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return false
		}
		sleep(ctx, probeMarkerPoll)
	}
}

// executeProbeRound mirrors the agent's probe loop: the whole round reports
// even when individual probes fail; the engine judges from the counts.
func executeProbeRound(ctx context.Context, controller Controller, channelID int64, model string, count, interval int, sleep func(context.Context, time.Duration)) (attempts, successes int, durationSum float64, lastError string) {
	if count < 1 {
		count = 1
	}
	for attempt := 0; attempt < count; attempt++ {
		if attempt > 0 && interval > 0 {
			sleep(ctx, time.Duration(interval)*time.Second)
		}
		if ctx.Err() != nil {
			lastError = ctx.Err().Error()
			break
		}
		probe, err := controller.Probe(ctx, channelID, model)
		attempts++
		if err == nil && probe.Success {
			successes++
			durationSum += probe.Duration
		} else if err != nil {
			lastError = err.Error()
		} else {
			lastError = probe.Message
		}
	}
	return attempts, successes, durationSum, lastError
}

// CreateTuningPreflight verifies the control path synchronously on direct
// sites: the returned command is already terminal, so the existing polling
// endpoint and auto-mode gate work unchanged with a first-poll result.
func (s Store) CreateTuningPreflight(siteID string, channelID int64, actor string, now time.Time) (storage.ChannelCommand, error) {
	controller, direct, err := s.controllerForSite(siteID)
	if err != nil {
		return storage.ChannelCommand{}, err
	}
	if !direct {
		return s.Store.CreateTuningPreflight(siteID, channelID, actor, now)
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	status, errorSummary := "succeeded", ""
	if _, err := controller.Update(ctx, channelcontrol.UpdateRequest{ChannelID: channelID}); err != nil {
		status, errorSummary = "failed", err.Error()
	}
	return s.Store.RecordTuningPreflightResult(siteID, channelID, actor, status, errorSummary, time.Now().UTC())
}
