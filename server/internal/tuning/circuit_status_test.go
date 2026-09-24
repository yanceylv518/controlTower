package tuning

import (
	"errors"
	"testing"
	"time"
)

func failedRound() *continuousFake {
	return &continuousFake{bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100}}, states: map[int64]ContinuousState{1: {ChannelID: 1, ModelName: "m", Phase: "probing", KError: 1, ProbeAttempts: 10}}}
}

func TestAllFailedRoundDisablesThenProbesAndEnables(t *testing.T) {
	f := failedRound()
	now := time.Now().UTC()
	p := autoPolicy()
	e := NewEngine(f)
	e.evaluateContinuous("i", p, now, f)
	s := f.states[1]
	if len(f.writes) != 1 || f.writes[0].ProposedChannelStatus == nil || *f.writes[0].ProposedChannelStatus != 2 || !s.CircuitDisabled || s.Phase != "circuit" {
		t.Fatalf("disable: %+v writes=%+v", s, f.writes)
	}
	e.evaluateContinuous("i", p, now.Add(4*time.Minute), f)
	if len(f.probes) != 0 {
		t.Fatal("probe sent before silence elapsed")
	}
	e.evaluateContinuous("i", p, now.Add(5*time.Minute), f)
	s = f.states[1]
	if len(f.probes) != 1 || s.Phase != "probing" {
		t.Fatalf("disabled channel stopped probing: %+v", s)
	}
	s.ProbeCommandID = nil
	s.ProbeAttempts = 10
	s.ProbeSuccesses = 0
	f.states[1] = s
	e.evaluateContinuous("i", p, now.Add(6*time.Minute), f)
	if len(f.writes) != 1 || !f.states[1].CircuitDisabled {
		t.Fatal("failed disabled round must remain disabled without duplicate disable")
	}
	e.evaluateContinuous("i", p, now.Add(11*time.Minute), f)
	s = f.states[1]
	s.ProbeCommandID = nil
	s.ProbeAttempts = 10
	s.ProbeSuccesses = 10
	s.ProbeDurationSum = 10
	f.states[1] = s
	e.evaluateContinuous("i", p, now.Add(12*time.Minute), f)
	s = f.states[1]
	rec := f.writes[len(f.writes)-1]
	if s.CircuitDisabled || s.Phase != "soft_start" || rec.ProposedChannelStatus == nil || *rec.ProposedChannelStatus != 1 || rec.ProposedWeight != 20 {
		t.Fatalf("recovery: %+v write=%+v", s, rec)
	}
}

func TestPartialOrInterruptedProbeDoesNotDisable(t *testing.T) {
	for _, tc := range []struct{ attempts, successes int }{{10, 1}, {9, 0}, {0, 0}} {
		f := failedRound()
		s := f.states[1]
		s.ProbeAttempts = tc.attempts
		s.ProbeSuccesses = tc.successes
		f.states[1] = s
		NewEngine(f).evaluateContinuous("i", autoPolicy(), time.Now(), f)
		if f.states[1].CircuitDisabled {
			t.Fatalf("not a fully failed round: %+v", tc)
		}
	}
}

func TestCircuitStatusWaitsForAgentAndRetriesFailure(t *testing.T) {
	f := failedRound()
	f.commandStatus = "pending"
	now := time.Now().UTC()
	e := NewEngine(f)
	p := autoPolicy()
	e.evaluateContinuous("i", p, now, f)
	if f.states[1].CircuitStatusCommandID == "" || f.states[1].CircuitStatusTarget != 2 {
		t.Fatal("must persist pending disable")
	}
	e.evaluateContinuous("i", p, now.Add(time.Minute), f)
	if len(f.writes) != 1 || len(f.probes) != 0 {
		t.Fatal("pending write was duplicated or probing started early")
	}
	f.commandStatus = "failed"
	e.evaluateContinuous("i", p, now.Add(2*time.Minute), f)
	if f.states[1].CircuitStatusTarget != 2 || f.states[1].CircuitStatusCommandID != "" {
		t.Fatal("failed disable was treated as success")
	}
	f.commandStatus = "succeeded"
	e.evaluateContinuous("i", p, now.Add(3*time.Minute), f)
	s := f.states[1]
	if s.CircuitStatusTarget != 0 || s.Phase != "circuit" {
		t.Fatalf("disable retry: %+v", s)
	}
	s.Phase = "probing"
	s.ProbeAttempts = 10
	s.ProbeSuccesses = 10
	f.states[1] = s
	f.commandStatus = "delivered"
	e.evaluateContinuous("i", p, now.Add(4*time.Minute), f)
	if !f.states[1].CircuitDisabled || f.states[1].Phase == "soft_start" {
		t.Fatal("enable enqueue was treated as applied")
	}
	f.commandStatus = "succeeded"
	e.evaluateContinuous("i", p, now.Add(5*time.Minute), f)
	if f.states[1].CircuitDisabled || f.states[1].Phase != "soft_start" {
		t.Fatal("enable acknowledgement did not recover")
	}
}

func TestCircuitStatusWriteFailureKeepsIntentAndObserveDoesNotWrite(t *testing.T) {
	f := failedRound()
	f.writeErr = errors.New("offline")
	now := time.Now()
	p := autoPolicy()
	e := NewEngine(f)
	e.evaluateContinuous("i", p, now, f)
	if !f.states[1].CircuitDisabled || f.states[1].CircuitStatusTarget != 2 {
		t.Fatal("lost disable intent")
	}
	p.Policy.DispatchModes["m"] = "observe"
	f.writeErr = nil
	e.evaluateContinuous("i", p, now.Add(time.Minute), f)
	if len(f.writes) != 0 || f.states[1].CircuitStatusTarget != 2 {
		t.Fatal("observe executed/consumed status intent")
	}
	p.Policy.DispatchModes["m"] = "auto"
	e.evaluateContinuous("i", p, now.Add(2*time.Minute), f)
	if len(f.writes) != 1 || f.states[1].Phase != "circuit" {
		t.Fatal("auto did not resume disable")
	}
}

func TestFailedEnableRequiresFreshProbeRound(t *testing.T) {
	for _, queued := range []bool{false, true} {
		f := failedRound()
		s := f.states[1]
		s.CircuitDisabled, s.ProbeSuccesses = true, 10
		f.states[1] = s
		if queued {
			f.commandStatus = "failed"
		} else {
			f.writeErr = errors.New("status update failed")
		}
		now := time.Now()
		e := NewEngine(f)
		e.evaluateContinuous("i", autoPolicy(), now, f)
		s = f.states[1]
		if !s.CircuitDisabled || s.Phase != "circuit" || s.CircuitStatusTarget != 0 || s.ProbeSuccesses != 0 {
			t.Fatalf("queued=%v: stale recovery evidence retained: %+v", queued, s)
		}
		f.writeErr, f.commandStatus = nil, "succeeded"
		writes := len(f.writes)
		e.evaluateContinuous("i", autoPolicy(), now.Add(5*time.Minute), f)
		if len(f.writes) != writes || len(f.probes) != 1 {
			t.Fatal("must probe again before enabling")
		}
	}
}

func TestZeroBaseTemporarilySuspendsDisabledRecoveryWithoutErasingIt(t *testing.T) {
	f := failedRound()
	now := time.Now()
	e := NewEngine(f)
	e.evaluateContinuous("i", autoPolicy(), now, f)
	f.bases[0].BaseWeight = 0
	e.evaluateContinuous("i", autoPolicy(), now.Add(time.Minute), f)
	if f.states[1].Phase != "circuit" || f.states[1].NextProbeAt == nil {
		t.Fatal("zero base erased disabled recovery")
	}
	f.bases[0].BaseWeight = 100
	e.evaluateContinuous("i", autoPolicy(), now.Add(5*time.Minute), f)
	if len(f.probes) != 1 || f.states[1].Phase != "probing" {
		t.Fatal("restoring base did not resume probes")
	}
}

func TestProbeCompletenessUsesDispatchedCountNotEditedPolicy(t *testing.T) {
	for _, tc := range []struct {
		sent, attempts, current int
		disabled                bool
	}{{10, 5, 5, false}, {5, 5, 10, true}} {
		f := failedRound()
		f.expectedProbeCount = tc.sent
		s := f.states[1]
		s.ProbeAttempts = tc.attempts
		f.states[1] = s
		p := autoPolicy()
		p.Policy.Continuous.ProbeCount = tc.current
		NewEngine(f).evaluateContinuous("i", p, time.Now(), f)
		if f.states[1].CircuitDisabled != tc.disabled {
			t.Fatalf("count edited during round: %+v state=%+v", tc, f.states[1])
		}
	}
}

func TestDisabledChannelDoesNotDistortPeerPerformanceBaseline(t *testing.T) {
	f := failedRound()
	now := time.Now()
	due := now.Add(time.Minute)
	s := f.states[1]
	s.CircuitDisabled = true
	s.Phase = "circuit"
	s.NextProbeAt = &due
	f.states[1] = s
	for _, id := range []int64{1, 2, 3} {
		if id != 1 {
			f.bases = append(f.bases, ChannelBaseValue{ChannelID: id, ModelName: "m", Models: []string{"m"}, BaseWeight: 100})
		}
		speed := 1.0
		if id == 1 {
			speed = 100
		}
		f.metrics = append(f.metrics, ChannelMetric{ChannelID: id, RequestCount: 100, TTFTP50: speed, TTFTP90: speed, TTFTP95: speed, SpeedSamples: 100, SpeedTTFTP50: speed, SpeedTTFTP90: speed, SpeedTTFTP95: speed})
	}
	NewEngine(f).evaluateContinuous("i", autoPolicy(), now, f)
	if f.states[2].BaselineTTFTP95 != 1 || f.states[3].BaselineTTFTP95 != 1 {
		t.Fatalf("disabled channel affected peers: %+v", f.states)
	}
}
