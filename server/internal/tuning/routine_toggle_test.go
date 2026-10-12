package tuning

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"controltower/internal/channelcontrol"
	"github.com/stretchr/testify/require"
)

func TestRoutineProbeToggleDefaultsAndRoundTrip(t *testing.T) {
	for _, raw := range []string{`{}`, `{"continuous":{"min_samples":5}}`} {
		p, err := DecodePolicyJSON([]byte(raw))
		require.NoError(t, err)
		require.True(t, p.Continuous.Rules().RoutineProbeEnabled)
		p.Continuous.RoutineProbeEnabled = false
		encoded, err := json.Marshal(p)
		require.NoError(t, err)
		require.Contains(t, string(encoded), `"routine_probe_enabled":false`)
		round, err := DecodePolicyJSON(encoded)
		require.NoError(t, err)
		require.False(t, round.Continuous.Rules().RoutineProbeEnabled)
		require.Empty(t, round.Validate())
	}
}

func TestDisabledRoutineProbeDoesNotLaunchOrApplyLateFault(t *testing.T) {
	f, e, base, oldPolicy := routineFixture()
	policy := oldPolicy
	policy.Policy.Continuous.RoutineProbeEnabled = false
	f.policy = &policy
	require.False(t, routineEligible(base, f.states[1], policy))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var calls atomic.Int32
	prober := routineTestProber{probe: func(context.Context) (channelcontrol.ProbeResult, error) {
		calls.Add(1)
		return channelcontrol.ProbeResult{}, nil
	}}
	runRoutineChannelWithPolicy(ctx, prober, 1, "m", make(chan struct{}, 4), routineTiming{time.Millisecond, time.Second}, policy.Policy.Continuous, func(routineStreak, time.Time) bool { return true })
	require.Zero(t, calls.Load(), "neither scheduled nor accelerated probes may run")
	// Same timestamp deliberately exercises the explicit latest-policy gate.
	e.applyRoutineEvidence(context.Background(), prober, channelKey{"i", 1}, base, oldPolicy, 0, routineStreak{errors: 3}, time.Now())
	require.Empty(t, f.writes)
	require.False(t, f.states[1].RoutineCircuitLatched)
}

type toggleSpeedStore struct {
	*probeSpeedFake
	queries atomic.Int32
}

func (f *toggleSpeedStore) QueryRoutineProbeSpeeds(ctx context.Context, site string, from, to time.Time) (map[int64]ProbeSpeedStats, error) {
	f.queries.Add(1)
	return f.probeSpeedFake.QueryRoutineProbeSpeeds(ctx, site, from, to)
}

func TestDisabledRoutineProbeExcludesStoredSamplesButKeepsTrafficWeighting(t *testing.T) {
	f, _, _, policy := routineFixture()
	policy.Policy.Continuous.MinSamples = 5
	policy.Policy.Continuous.RoutineProbeEnabled = false
	f.policy = &policy
	f.bases = append(f.bases, ChannelBaseValue{ChannelID: 2, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 100})
	now := time.Now().UTC()
	stats := map[int64]ProbeSpeedStats{1: {Samples: 5, MeanSeconds: 1, LastStartedAt: now}, 2: {Samples: 5, MeanSeconds: 4, LastStartedAt: now}}
	s := &toggleSpeedStore{probeSpeedFake: &probeSpeedFake{f, stats}}
	previous := f.states[1]
	previous.ProbeSpeed = ProbeSpeedStats{Samples: 5, MeanSeconds: 1, Factor: 1.2, Ready: true, LastStartedAt: now}
	f.states[1] = previous
	e := NewEngine(s)
	e.evaluateContinuous("i", policy, now, s)
	require.Zero(t, s.queries.Load(), "disabled evidence must not query the sample store")
	require.False(t, f.states[1].ProbeSpeed.Ready)
	require.Equal(t, 1.0, f.states[1].ProbeSpeed.Factor)
	require.False(t, f.states[1].Evaluation.PerformanceEvaluated)
	require.Zero(t, f.states[1].ProposedWeight, "probe-only evidence must not raise zero")
	require.Empty(t, f.writes)
	// Production evidence still raises a performance-zero channel normally.
	f.metrics = []ChannelMetric{
		{ChannelID: 1, RequestCount: 20, OTPSStatsVersion: 1, OTPSSamples: 20, OTPSSampleTokens: 1000, OTPS: 100},
		{ChannelID: 2, RequestCount: 20, OTPSStatsVersion: 1, OTPSSamples: 20, OTPSSampleTokens: 1000, OTPS: 10},
	}
	e.evaluateContinuous("i", policy, now.Add(time.Minute), s)
	require.True(t, f.states[1].Evaluation.PerformanceEvaluated)
	require.Positive(t, f.states[1].ProposedWeight)
	require.NotEmpty(t, f.writes)
	require.False(t, f.states[1].ProbeSpeed.Ready)
	require.Equal(t, 1.0, f.states[1].ProbeSpeed.Factor)
	require.Zero(t, s.queries.Load())
}

func TestDisabledRoutineProbeIgnoresCachedFactorAndBaseline(t *testing.T) {
	p := DefaultPolicy().Continuous
	p.RoutineProbeEnabled = false
	now := time.Now()
	s := ProbeSpeedStats{Samples: 20, MeanSeconds: 1, LastStartedAt: now, Ready: true, Factor: 1.2}
	rows := []ChannelBaseValue{{ChannelID: 1, Models: []string{"m"}, BaseWeight: 100}, {ChannelID: 2, Models: []string{"m"}, BaseWeight: 100}}
	require.Zero(t, probeSpeedBaseline(rows, map[int64]ProbeSpeedStats{1: s, 2: s}, nil, 20, now, p))
	got := evaluateProbeSpeed(s, 2, p, now)
	require.False(t, got.Ready)
	require.Equal(t, 1.0, got.Factor)
	require.InDelta(t, 1.1*.9*1.05*.8, combinedFactor(ContinuousState{KSpeed: 1.1, KCache: .9, KOTPS: 1.05, KError: .8, ProbeSpeed: s}, p), 1e-10)
}

func TestDisabledRoutineProbeStillCompletesCircuitRecovery(t *testing.T) {
	f, e, base, policy := routineFixture()
	e.applyRoutineEvidence(context.Background(), routineTestProber{}, channelKey{"i", 1}, base, policy, 0, routineStreak{errors: 3}, time.Now())
	state := f.states[1]
	require.True(t, state.RoutineCircuitLatched)
	require.NotNil(t, state.NextProbeAt)
	policy.Policy.Continuous.RoutineProbeEnabled = false
	f.policy = &policy
	now := state.NextProbeAt.Add(time.Second)
	e.evaluateContinuous("i", policy, now, f)
	state = f.states[1]
	require.Equal(t, "probing", state.Phase)
	require.True(t, state.RoutineCircuitLatched)
	require.NotEmpty(t, f.probes, "recovery requests must remain enabled")
	state.ProbeCommandID = nil
	state.ProbeAttempts, state.ProbeSuccesses, state.ProbeDurationSum = 10, 10, 10
	f.states[1] = state
	e.evaluateContinuous("i", policy, now.Add(time.Minute), f)
	require.Equal(t, "soft_start", f.states[1].Phase)
	require.False(t, f.states[1].RoutineCircuitLatched)
	require.False(t, f.states[1].CircuitDisabled)
	require.Equal(t, int64(20), f.states[1].ProposedWeight)
}

type toggleRoutineStore struct {
	*continuousFake
	enabled atomic.Bool
	prober  RoutineProber
}

func (f *toggleRoutineStore) ListEnabledSites() ([]string, error) { return []string{"i"}, nil }
func (f *toggleRoutineStore) GetPolicy(string) (PolicyRecord, bool, error) {
	p := *f.policy
	p.Policy.Continuous.RoutineProbeEnabled = f.enabled.Load()
	return p, true, nil
}
func (f *toggleRoutineStore) RoutineProbeController(string) (RoutineProber, string, error) {
	return f.prober, "test", nil
}

func TestRoutineSchedulerCancelsInFlightWhenDisabled(t *testing.T) {
	f, _, _, policy := routineFixture()
	policy.Policy.Continuous.RoutineIntervalSeconds = 1
	f.policy = &policy
	started, canceled := make(chan struct{}, 20), make(chan struct{}, 20)
	s := &toggleRoutineStore{continuousFake: f, prober: routineTestProber{probe: func(ctx context.Context) (channelcontrol.ProbeResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		return channelcontrol.ProbeResult{}, ctx.Err()
	}}}
	s.enabled.Store(true)
	e := NewEngine(s)
	e.probeCoverage = newProbeCoverageBuffer()
	e.probeRecords = newProbeRecordBuffer()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); e.runRoutineProbes(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("enabled probe did not start")
	}
	s.enabled.Store(false)
	select {
	case <-canceled:
	case <-time.After(7 * time.Second):
		t.Fatal("policy reconciliation did not cancel in-flight probes")
	}
	require.Eventually(t, func() bool {
		rows, _ := e.probeRecords.snapshot()
		if len(rows) == 0 {
			return false
		}
		for _, r := range rows {
			if r.Outcome != "canceled" {
				return false
			}
		}
		return true
	}, time.Second, 10*time.Millisecond)
	rows, _ := e.probeRecords.snapshot()
	time.Sleep(1100 * time.Millisecond) // A complete regular interval after cancellation.
	after, _ := e.probeRecords.snapshot()
	require.Len(t, after, len(rows), "disabled workers must not dispatch new requests")
	cancel()
	<-done
	require.Empty(t, f.writes)
	require.False(t, f.states[1].RoutineCircuitLatched)
}
