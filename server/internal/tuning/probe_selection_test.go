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

func TestRoutineProbeSelectionPolicy(t *testing.T) {
	p, err := DecodePolicyJSON([]byte(`{"continuous":{"min_samples":5}}`))
	require.NoError(t, err)
	require.True(t, p.Continuous.RoutineProbeEnabledFor(303), "legacy policies keep all channels")
	p.Continuous.RoutineProbeSelectedOnly = true
	require.False(t, p.Continuous.RoutineProbeEnabledFor(303), "empty selection means none")
	p.Continuous.RoutineProbeChannelIDs = []int64{303, 404}
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	next, err := DecodePolicyJSON(raw)
	require.NoError(t, err)
	require.Empty(t, next.Validate())
	require.True(t, next.Continuous.RoutineProbeEnabledFor(303))
	require.False(t, next.Continuous.RoutineProbeEnabledFor(505))
	next.Continuous.RoutineProbeEnabled = false
	require.False(t, next.Continuous.RoutineProbeEnabledFor(303))
	for _, ids := range [][]int64{{0}, {-1}, {303, 303}, {9007199254740992}} {
		p.Continuous.RoutineProbeChannelIDs = ids
		require.Contains(t, p.Validate(), "continuous.routine_probe_channel_ids")
	}
}

func TestRoutineProbeSelectionStopsLaunchAndLateFault(t *testing.T) {
	f, e, base, old := routineFixture()
	next := old
	next.Policy.Continuous.RoutineProbeSelectedOnly = true
	next.Policy.Continuous.RoutineProbeChannelIDs = []int64{2}
	f.policy = &next
	require.False(t, routineEligible(base, f.states[1], next))
	var calls atomic.Int32
	prober := routineTestProber{probe: func(context.Context) (channelcontrol.ProbeResult, error) {
		calls.Add(1)
		return channelcontrol.ProbeResult{}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	runRoutineChannelWithPolicy(ctx, prober, 1, "m", make(chan struct{}, 4), routineTiming{time.Millisecond, time.Second}, next.Policy.Continuous, func(routineStreak, time.Time) bool { return true })
	require.Zero(t, calls.Load())
	e.applyRoutineEvidence(context.Background(), prober, channelKey{"i", 1}, base, old, 0, routineStreak{errors: 3}, time.Now())
	require.Empty(t, f.writes)
	require.False(t, f.states[1].RoutineCircuitLatched)
}

func TestRoutineProbeSelectionExcludesCachedEvidence(t *testing.T) {
	f, _, _, p := routineFixture()
	p.Policy.Continuous.MinSamples = 5
	p.Policy.Continuous.RoutineProbeSelectedOnly = true
	p.Policy.Continuous.RoutineProbeChannelIDs = []int64{2, 3}
	f.policy = &p
	now := time.Now().UTC()
	for _, id := range []int64{2, 3} {
		f.bases = append(f.bases, ChannelBaseValue{ChannelID: id, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 100})
	}
	stats := map[int64]ProbeSpeedStats{1: {Samples: 5, MeanSeconds: 100, LastStartedAt: now}, 2: {Samples: 5, MeanSeconds: 2, LastStartedAt: now}, 3: {Samples: 5, MeanSeconds: 4, LastStartedAt: now}}
	require.Equal(t, 3.0, probeSpeedBaseline(f.bases, stats, nil, 5, now, p.Policy.Continuous))
	previous := f.states[1]
	previous.ProbeSpeed = ProbeSpeedStats{Samples: 5, MeanSeconds: 1, Ready: true, Factor: 1.2, LastStartedAt: now}
	f.states[1] = previous
	s := &probeSpeedFake{f, stats}
	e := NewEngine(s)
	e.evaluateContinuous("i", p, now, s)
	require.False(t, f.states[1].ProbeSpeed.Ready)
	require.Equal(t, 1.0, f.states[1].ProbeSpeed.Factor)
	require.False(t, f.states[1].Evaluation.PerformanceEvaluated)
	require.Zero(t, f.states[1].ProposedWeight)
	require.True(t, f.states[2].ProbeSpeed.Ready)
	require.Equal(t, 3.0, f.states[2].ProbeSpeed.BaselineSeconds)
	require.Len(t, stats, 3, "store-owned samples must not be mutated")
	f.metrics = []ChannelMetric{{ChannelID: 1, RequestCount: 20, OTPSStatsVersion: 1, OTPSSamples: 20, OTPSSampleTokens: 1000, OTPS: 100}, {ChannelID: 2, RequestCount: 20, OTPSStatsVersion: 1, OTPSSamples: 20, OTPSSampleTokens: 1000, OTPS: 10}}
	e.evaluateContinuous("i", p, now.Add(time.Minute), s)
	require.True(t, f.states[1].Evaluation.PerformanceEvaluated)
	require.Positive(t, f.states[1].ProposedWeight)
}

func TestRoutineProbeSelectionKeepsCircuitRecovery(t *testing.T) {
	f, e, base, p := routineFixture()
	e.applyRoutineEvidence(context.Background(), routineTestProber{}, channelKey{"i", 1}, base, p, 0, routineStreak{errors: 3}, time.Now())
	next := f.states[1].NextProbeAt.Add(time.Second)
	p.Policy.Continuous.RoutineProbeSelectedOnly = true // no routine channels
	f.policy = &p
	e.evaluateContinuous("i", p, next, f)
	require.Equal(t, "probing", f.states[1].Phase)
	require.NotEmpty(t, f.probes)
	state := f.states[1]
	state.ProbeCommandID = nil
	state.ProbeAttempts, state.ProbeSuccesses, state.ProbeDurationSum = 10, 10, 10
	f.states[1] = state
	e.evaluateContinuous("i", p, next.Add(time.Minute), f)
	require.Equal(t, "soft_start", f.states[1].Phase)
	require.False(t, f.states[1].CircuitDisabled)
}

type selectionRoutineStore struct{ *toggleRoutineStore }

func (f *selectionRoutineStore) GetPolicy(string) (PolicyRecord, bool, error) {
	p := *f.policy
	p.Policy.Continuous.RoutineProbeSelectedOnly = true
	p.Policy.Continuous.RoutineProbeChannelIDs = nil
	if f.enabled.Load() {
		p.Policy.Continuous.RoutineProbeChannelIDs = []int64{1}
	}
	return p, true, nil
}

func TestRoutineProbeSelectionCancelsInFlight(t *testing.T) {
	f, _, _, p := routineFixture()
	p.Policy.Continuous.RoutineIntervalSeconds = 1
	f.policy = &p
	started, canceled := make(chan struct{}, 20), make(chan struct{}, 20)
	s := &selectionRoutineStore{&toggleRoutineStore{continuousFake: f, prober: routineTestProber{probe: func(ctx context.Context) (channelcontrol.ProbeResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		return channelcontrol.ProbeResult{}, ctx.Err()
	}}}}
	s.enabled.Store(true)
	e := NewEngine(s)
	e.probeRecords = newProbeRecordBuffer()
	e.probeCoverage = newProbeCoverageBuffer()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); e.runRoutineProbes(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("selected channel did not start")
	}
	s.enabled.Store(false)
	select {
	case <-canceled:
	case <-time.After(7 * time.Second):
		t.Fatal("removed channel was not canceled")
	}
	cancel()
	<-done
	require.Empty(t, f.writes)
	require.False(t, f.states[1].RoutineCircuitLatched)
}
