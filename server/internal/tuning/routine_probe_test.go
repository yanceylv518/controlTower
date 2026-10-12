package tuning

import (
	"context"
	"controltower/internal/channelcontrol"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

type routineTestProber struct {
	probe    func(context.Context) (channelcontrol.ProbeResult, error)
	checkErr error
}

func (p routineTestProber) Probe(ctx context.Context, _ int64, _ string) (channelcontrol.ProbeResult, error) {
	return p.probe(ctx)
}
func (p routineTestProber) Check(context.Context) error { return p.checkErr }

func TestRoutineProbeDeadlinesOverlapAndIgnoreLateSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	p := routineTestProber{probe: func(context.Context) (channelcontrol.ProbeResult, error) {
		calls.Add(1)
		<-release
		return channelcontrol.ProbeResult{Success: true}, nil
	}}
	started := time.Now()
	var observed routineStreak
	runRoutineChannel(ctx, p, 7, "m", make(chan struct{}, 8), routineTiming{100 * time.Millisecond, 300 * time.Millisecond}, func(s routineStreak, _ time.Time) bool { observed = s; return false })
	if observed.slow != 2 || observed.healthy != 0 || calls.Load() < 3 {
		t.Fatalf("streak=%+v calls=%d", observed, calls.Load())
	}
	if elapsed := time.Since(started); elapsed < 390*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("deadline did not overlap: %v", elapsed)
	}
}

func TestRoutineProbeFirstErrorImmediatelyConfirmsAndStopsAtThree(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var calls atomic.Int32
	p := routineTestProber{probe: func(context.Context) (channelcontrol.ProbeResult, error) {
		calls.Add(1)
		return channelcontrol.ProbeResult{}, nil
	}}
	var observed routineStreak
	runRoutineChannel(ctx, p, 7, "m", make(chan struct{}, 8), routineTiming{time.Hour, time.Second}, func(s routineStreak, _ time.Time) bool { observed = s; return false })
	if observed.errors != 3 || calls.Load() != 3 {
		t.Fatalf("streak=%+v calls=%d", observed, calls.Load())
	}
}

func TestRoutineProbeAdjacentErrorsDoNotWaitForEarlierRequest(t *testing.T) {
	for _, limit := range []int{3, 6} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			release := make(chan struct{})
			defer close(release)
			var calls atomic.Int32
			p := routineTestProber{probe: func(context.Context) (channelcontrol.ProbeResult, error) {
				if calls.Add(1) == 1 {
					<-release
					return channelcontrol.ProbeResult{Success: true}, nil
				}
				return channelcontrol.ProbeResult{}, nil
			}}
			policy := DefaultPolicy().Continuous
			policy.RoutineErrorLimit = limit
			done := make(chan routineStreak, 1)
			go func() {
				runRoutineChannelWithPolicy(ctx, p, 7, "m", make(chan struct{}, 8), routineTiming{20 * time.Millisecond, time.Second}, policy, func(s routineStreak, _ time.Time) bool { done <- s; return false })
			}()
			select {
			case s := <-done:
				if s.errors != limit || calls.Load() != int32(limit+1) {
					t.Fatalf("streak=%+v calls=%d", s, calls.Load())
				}
			case <-time.After(700 * time.Millisecond):
				t.Fatal("adjacent errors waited for an unrelated earlier request")
			}
		})
	}
}

func TestRoutineFaultSegmentsRespectGapsAndResetResults(t *testing.T) {
	p := DefaultPolicy().Continuous
	for _, tc := range []struct {
		name   string
		values map[int]routineOutcome
		prefix routineStreak
		want   bool
	}{
		{"three after gap", map[int]routineOutcome{1: routineError, 2: routineError, 3: routineError}, routineStreak{}, true},
		{"gap inside errors", map[int]routineOutcome{0: routineError, 2: routineError, 3: routineError}, routineStreak{}, false},
		{"success interrupts", map[int]routineOutcome{0: routineError, 1: routineHealthy, 2: routineError, 3: routineError}, routineStreak{}, false},
		{"unknown interrupts", map[int]routineOutcome{0: routineError, 1: routineUnknown, 2: routineError, 3: routineError}, routineStreak{}, false},
		{"channel error preserves slow evidence", map[int]routineOutcome{0: routineSlow, 1: routineError, 2: routineSlow, 3: routineError}, routineStreak{}, true},
		{"two slow after gap", map[int]routineOutcome{2: routineSlow, 3: routineSlow}, routineStreak{}, true},
		{"folded prefix", map[int]routineOutcome{0: routineError}, routineStreak{errors: 2}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results := map[int]routineResult{}
			for sequence, outcome := range tc.values {
				results[sequence] = routineResult{sequence: sequence, outcome: outcome}
			}
			_, _, trip := confirmedRoutineFault(results, 0, 4, tc.prefix, p)
			if trip != tc.want {
				t.Fatalf("trip=%v want %v", trip, tc.want)
			}
		})
	}
}

func TestRoutineFaultCountersAreIndependentAndResetOnSuccess(t *testing.T) {
	for _, tc := range []struct {
		name     string
		outcomes []routineOutcome
		tripAt   int
	}{
		{"slow error slow", []routineOutcome{routineSlow, routineError, routineSlow}, 2},
		{"two intervening errors", []routineOutcome{routineSlow, routineError, routineError, routineSlow}, 3},
		{"healthy resets latency", []routineOutcome{routineSlow, routineError, routineHealthy, routineSlow}, -1},
		{"slow preserves error evidence", []routineOutcome{routineError, routineSlow, routineError, routineError}, 3},
		{"healthy resets both counters", []routineOutcome{routineError, routineSlow, routineHealthy, routineError, routineError}, -1},
		{"unknown breaks coverage", []routineOutcome{routineSlow, routineUnknown, routineSlow}, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s routineStreak
			for i, o := range tc.outcomes {
				if got := s.add(o); got != (i == tc.tripAt) {
					t.Fatalf("step=%d trip=%v streak=%+v", i, got, s)
				}
			}
		})
	}
}

func TestRoutineProbeCancellationAndTransportFailureAreNotChannelErrors(t *testing.T) {
	for _, transport := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		p := routineTestProber{probe: func(ctx context.Context) (channelcontrol.ProbeResult, error) {
			if !transport {
				<-ctx.Done()
			}
			return channelcontrol.ProbeResult{}, errors.New("connection unavailable")
		}}
		called := false
		runRoutineChannel(ctx, p, 7, "m", make(chan struct{}, 8), routineTiming{5 * time.Millisecond, time.Second}, func(routineStreak, time.Time) bool { called = true; return false })
		cancel()
		if called {
			t.Fatal("infrastructure/cancellation produced channel evidence")
		}
	}
}

func routineFixture() (*continuousFake, *Engine, ChannelBaseValue, PolicyRecord) {
	policy := autoPolicy()
	policy.UpdatedAt = time.Now().Add(-time.Minute)
	base := ChannelBaseValue{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 0, UpdatedAt: policy.UpdatedAt}
	f := &continuousFake{bases: []ChannelBaseValue{base}, states: map[int64]ContinuousState{1: {InstanceID: "i", ChannelID: 1, ModelName: "m", Phase: "normal", KError: 1}}, policy: &policy}
	return f, NewEngine(f), base, policy
}

func TestHealthyRoutineProbesOnlyRecordSamplesWithoutRecoveryCallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	samples := 0
	p := routineTestProber{probe: func(context.Context) (channelcontrol.ProbeResult, error) {
		return channelcontrol.ProbeResult{Success: true}, nil
	}}
	runRoutineChannel(ctx, p, 1, "m", make(chan struct{}, 4), routineTiming{time.Millisecond, time.Second}, func(routineStreak, time.Time) bool {
		t.Fatal("healthy probes must not trigger a separate recovery write")
		return false
	}, func(r routineResult) {
		if r.outcome != routineHealthy || !r.scheduled {
			t.Fatal("healthy scheduled sample lost")
		}
		samples++
		if samples == 6 {
			cancel()
		}
	})
	if samples != 6 {
		t.Fatalf("got %d samples; healthy sampling must continue after three successes", samples)
	}
}

func TestRoutineFaultPersistsAcrossWriteFailureAndCannotRaiseWeight(t *testing.T) {
	f, e, base, policy := routineFixture()
	f.writeErr = errors.New("write failed")
	e.applyRoutineEvidence(context.Background(), routineTestProber{}, channelKey{"i", 1}, base, policy, 0, routineStreak{slow: 2}, time.Now())
	s := f.states[1]
	if !s.RoutineCircuitLatched || !s.CircuitDisabled || s.CircuitStatusTarget != 2 || s.Phase != "circuit" {
		t.Fatalf("fault lost: %+v", s)
	}
	s.WriteFailureStreak = 5
	failed := time.Now()
	s.LastWriteFailureAt = &failed
	e.finishFailedWrite(&s, base, policy.Policy.Continuous, failed.Add(11*time.Minute))
	if !s.RoutineCircuitLatched || s.CircuitStatusTarget != 2 || s.ProposedWeight != 0 || s.Phase != "circuit" {
		t.Fatalf("budget unlocked fault: %+v", s)
	}
	f.states[1] = s
	f.writeErr = nil
	NewEngine(f).evaluateContinuous("i", policy, failed.Add(12*time.Minute), f)
	for _, w := range f.writes {
		if w.ProposedWeight > 0 {
			t.Fatalf("fault was raised: %+v", w)
		}
	}
}

func TestRoutineHealthyEvidenceDoesNotRecoverZeroAndHonorsOwnershipChanges(t *testing.T) {
	f, e, base, policy := routineFixture()
	e.applyRoutineEvidence(context.Background(), routineTestProber{}, channelKey{"i", 1}, base, policy, 0, routineStreak{healthy: 3}, time.Now())
	if len(f.writes) != 0 || f.states[1].ProposedWeight != 0 || f.states[1].Phase != "normal" || f.states[1].RoutineCircuitLatched {
		t.Fatalf("zero recovery: %+v %+v", f.states[1], f.writes)
	}
	f, e, base, policy = routineFixture()
	s := f.states[1]
	s.ControlRevision = 1
	f.states[1] = s
	e.applyRoutineEvidence(context.Background(), routineTestProber{}, channelKey{"i", 1}, base, policy, 0, routineStreak{slow: 2}, time.Now())
	if len(f.writes) != 0 {
		t.Fatal("old evidence survived external takeover")
	}
	f, e, base, policy = routineFixture()
	e.applyRoutineEvidence(context.Background(), routineTestProber{checkErr: errors.New("admin offline")}, channelKey{"i", 1}, base, policy, 0, routineStreak{errors: 3}, time.Now())
	if len(f.writes) != 0 || f.states[1].RoutineCircuitLatched {
		t.Fatal("site outage disabled channel")
	}
}

func TestPerformanceFloorCanReachZeroAndOrdinaryEvaluationCanRaiseIt(t *testing.T) {
	f, e, _, policy := routineFixture()
	f.bases = []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 1, CurrentWeight: 1}, {ChannelID: 2, ModelName: "m", Models: []string{"m"}, BaseWeight: 1, CurrentWeight: 1}}
	f.metrics = []ChannelMetric{{ChannelID: 1, RequestCount: 20, OTPSStatsVersion: 1, OTPSSamples: 20, OTPSSampleTokens: 1000, OTPS: 10}, {ChannelID: 2, RequestCount: 20, OTPSStatsVersion: 1, OTPSSamples: 20, OTPSSampleTokens: 1000, OTPS: 100}}
	now := time.Now()
	e.evaluateContinuous("i", policy, now, f)
	if f.states[1].ProposedWeight != 0 || f.states[1].Phase != "normal" {
		t.Fatalf("performance floor: %+v", f.states[1])
	}
	f.metrics[0].OTPS = 200
	e.evaluateContinuous("i", policy, now.Add(time.Minute), f)
	if f.states[1].ProposedWeight < 1 || f.states[1].Phase != "normal" {
		t.Fatalf("ordinary rise blocked: %+v", f.states[1])
	}
}

func TestRoutineCircuitKeepsLatchUntilRecoveryEnableIsConfirmed(t *testing.T) {
	f, e, base, policy := routineFixture()
	e.applyRoutineEvidence(context.Background(), routineTestProber{}, channelKey{"i", 1}, base, policy, 0, routineStreak{errors: 3}, time.Now())
	s := f.states[1]
	if !s.RoutineCircuitLatched || s.NextProbeAt == nil || s.CircuitStatusTarget != 0 {
		t.Fatalf("disable was not confirmed: %+v", s)
	}
	now := s.NextProbeAt.Add(time.Second)
	e.evaluateContinuous("i", policy, now, f)
	s = f.states[1]
	if s.Phase != "probing" || !s.RoutineCircuitLatched {
		t.Fatalf("cooldown did not enter owned recovery: %+v", s)
	}
	s.ProbeCommandID = nil
	s.ProbeAttempts, s.ProbeSuccesses, s.ProbeDurationSum = 10, 10, 10
	f.states[1] = s
	f.commandStatus = "pending"
	e.evaluateContinuous("i", policy, now.Add(time.Minute), f)
	if !f.states[1].RoutineCircuitLatched || f.states[1].CircuitStatusTarget != 1 {
		t.Fatal("pending enable prematurely released fault latch")
	}
	f.commandStatus = "succeeded"
	e.evaluateContinuous("i", policy, now.Add(2*time.Minute), f)
	s = f.states[1]
	if s.RoutineCircuitLatched || s.CircuitDisabled || s.Phase != "soft_start" || s.ProposedWeight != 20 {
		t.Fatalf("confirmed enable did not release latch: %+v", s)
	}
}
