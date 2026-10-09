package tuning

import (
	"errors"
	"testing"
	"time"
)

func TestFailedDisableEndsAfterFiveFailuresAndReturnsToNormalEvaluation(t *testing.T) {
	f := failedRound()
	f.writeErr = errors.New("status unavailable")
	now := time.Now().UTC()
	for _, minutes := range []int{0, 1, 2, 12, 22} {
		NewEngine(f).evaluateContinuous("i", autoPolicy(), now.Add(time.Duration(minutes)*time.Minute), f)
	}
	if f.writeAttempts != 5 || f.states[1].WriteFailureStreak != 5 {
		t.Fatalf("attempts=%d state=%+v", f.writeAttempts, f.states[1])
	}
	NewEngine(f).evaluateContinuous("i", autoPolicy(), now.Add(23*time.Minute), f)
	s := f.states[1]
	if f.writeAttempts != 5 || s.CircuitStatusTarget != 0 || s.CircuitDisabled || s.Phase != "normal" || s.PausedReason != "" {
		t.Fatalf("old disable task was not retired: attempts=%d state=%+v", f.writeAttempts, s)
	}
	f.writeErr = nil
	f.bases[0].CurrentWeight = 5
	addNeutralPerformanceEvidence(f)
	NewEngine(f).evaluateContinuous("i", autoPolicy(), now.Add(24*time.Minute), f)
	if len(f.writes) != 1 || f.writes[0].ProposedChannelStatus != nil || f.writes[0].ProposedWeight <= 0 {
		t.Fatalf("normal evaluation failed to resume: writes=%+v state=%+v", f.writes, f.states[1])
	}
	found := false
	for _, r := range f.recommendations {
		if r.Rule == "write_abandoned" {
			found = true
			if r.Status != "recorded" || r.Evidence["error"] == "" {
				t.Fatalf("bad abandonment record: %+v", r)
			}
		}
	}
	if !found {
		t.Fatal("missing failure history")
	}
}

func TestExhaustedConfirmedDisableKeepsFreshProbeRecovery(t *testing.T) {
	f := failedRound()
	s := f.states[1]
	s.CircuitDisabled = true
	s.CircuitStatusTarget = 1
	s.WriteFailureStreak = 5
	s.PausedReason = "write_failed"
	s.ProbeSuccesses = 10
	f.states[1] = s
	now := time.Now().UTC()
	NewEngine(f).evaluateContinuous("i", autoPolicy(), now, f)
	s = f.states[1]
	if !s.CircuitDisabled || s.CircuitStatusTarget != 0 || s.Phase != "circuit" || s.ProbeAttempts != 0 || s.NextProbeAt == nil || f.writeAttempts != 0 {
		t.Fatalf("confirmed closure lost or stale recovery reused: %+v", s)
	}
	NewEngine(f).evaluateContinuous("i", autoPolicy(), *s.NextProbeAt, f)
	if len(f.probes) != 1 || f.writeAttempts != 0 {
		t.Fatal("recovery must first obtain fresh probe evidence")
	}
}

func TestExhaustedTaskWaitsForInFlightReceipt(t *testing.T) {
	f := failedRound()
	s := f.states[1]
	s.CircuitDisabled = true
	s.CircuitStatusTarget = 2
	s.CircuitStatusCommandID = "inflight"
	s.WriteFailureStreak = 5
	s.PausedReason = "write_failed"
	f.states[1] = s
	f.commandStatus = "delivered"
	NewEngine(f).evaluateContinuous("i", autoPolicy(), time.Now(), f)
	if f.states[1].CircuitStatusCommandID != "inflight" || f.writeAttempts != 0 {
		t.Fatal("in-flight write was discarded or duplicated")
	}
}

func TestLegacyFailureCountCannotBypassRetryLimit(t *testing.T) {
	for _, count := range []int{5, 10} {
		for _, reason := range []string{"", "write_failed"} {
			s := ContinuousState{WriteFailureStreak: count, PausedReason: reason}
			if writeAttemptAllowed(s, time.Now().Add(365*24*time.Hour)) {
				t.Fatalf("legacy state allowed: %+v", s)
			}
		}
	}
}

func TestRecoveredWeightCannotBypassRetryLimit(t *testing.T) {
	f := failedRound()
	s := f.states[1]
	s.WriteFailureStreak = 5
	s.PausedReason = "write_failed"
	rec := Recommendation{InstanceID: "i", ChannelID: 1, Rule: "circuit_recovered", ProposedWeight: 20}
	_, err := NewEngine(f).createTrackedWeightChange(f, rec, f.bases[0], &s, time.Now())
	if !errors.Is(err, ErrDecisionSuperseded) || f.writeAttempts != 0 {
		t.Fatalf("recovery bypassed retry cap: err=%v attempts=%d", err, f.writeAttempts)
	}
}

func TestObserveExplicitlyResetsExhaustedCircuitRetry(t *testing.T) {
	f := failedRound()
	s := f.states[1]
	s.CircuitDisabled = true
	s.CircuitStatusTarget = 2
	s.PausedReason = "write_failed"
	s.WriteFailureStreak = 10
	s.LastWriteError = "Invalid parameters"
	f.states[1] = s
	now := time.Now().UTC()
	p := autoPolicy()
	p.Policy.DispatchModes["m"] = "observe"
	NewEngine(f).evaluateContinuous("i", p, now, f)
	s = f.states[1]
	if s.WriteFailureStreak != 0 || s.PausedReason != "" || !s.CircuitDisabled || s.CircuitStatusTarget != 2 || f.writeAttempts != 0 {
		t.Fatalf("observe reset must persist without writing or discarding ownership: %+v", s)
	}
	NewEngine(f).evaluateContinuous("i", autoPolicy(), now.Add(time.Minute), f)
	if f.writeAttempts != 1 {
		t.Fatal("explicit re-enable did not allow a new write")
	}
}

func TestObserveResetsFailureDuringMetricGap(t *testing.T) {
	f := failedRound()
	s := f.states[1]
	s.Phase = "normal"
	s.LastObservedRequests = 100
	s.WriteFailureStreak = 5
	s.PausedReason = "write_failed"
	f.states[1] = s
	p := autoPolicy()
	p.Policy.DispatchModes["m"] = "observe"
	NewEngine(f).evaluateContinuous("i", p, time.Now(), f)
	if f.states[1].WriteFailureStreak != 0 {
		t.Fatal("metric gap blocked operator reset")
	}
}
