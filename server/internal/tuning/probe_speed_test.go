package tuning

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"controltower/internal/channelcontrol"
	"github.com/stretchr/testify/require"
)

type probeSpeedFake struct {
	*continuousFake
	speeds map[int64]ProbeSpeedStats
}

func (f *probeSpeedFake) RecordRoutineProbeSample(context.Context, RoutineProbeSample) error {
	return nil
}
func (f *probeSpeedFake) PruneRoutineProbeSamples(context.Context, time.Time) error { return nil }
func (f *probeSpeedFake) QueryRoutineProbeSpeeds(context.Context, string, time.Time, time.Time) (map[int64]ProbeSpeedStats, error) {
	return f.speeds, nil
}

func TestProbeSpeedRanksWithoutProductionSamplesAndExpires(t *testing.T) {
	f, _, _, policy := routineFixture()
	policy.Policy.Continuous.MinSamples = 5
	policy.Policy.Continuous.MaxIncreasePercent = 100
	f.policy = &policy
	f.bases = []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 0}, {ChannelID: 2, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 100}}
	now := time.Now().UTC()
	s := &probeSpeedFake{continuousFake: f, speeds: map[int64]ProbeSpeedStats{1: {Samples: 5, MeanSeconds: 1, LastStartedAt: now.Add(-10 * time.Second)}, 2: {Samples: 5, MeanSeconds: 4, LastStartedAt: now.Add(-10 * time.Second)}}}
	e := NewEngine(s)
	e.evaluateContinuous("i", policy, now, s)
	require.True(t, f.states[1].ProbeSpeed.Ready)
	require.Equal(t, int64(120), f.states[1].ProposedWeight, "probe-only evidence can raise zero through ordinary evaluation")
	require.Equal(t, int64(math.Floor(100*math.Pow(2.5/4, .25))), f.states[2].ProposedWeight)
	require.True(t, f.states[1].Evaluation.PerformanceEvaluated)
	require.Equal(t, float64(1), f.states[1].KSpeed)
	require.Equal(t, float64(1), f.states[1].KOTPS)
	e.evaluateContinuous("i", policy, now.Add(2*time.Minute), s)
	require.False(t, f.states[1].ProbeSpeed.Ready)
	require.Equal(t, float64(1), f.states[1].ProbeSpeed.Factor)
	require.Equal(t, int64(120), f.states[1].ProposedWeight, "expired samples must not reset the applied weight")
}

func TestProbeSpeedBaselineAndFactorGuards(t *testing.T) {
	now := time.Now()
	p := DefaultPolicy().Continuous
	p.MinSamples = 5
	rows := []ChannelBaseValue{{ChannelID: 1, Models: []string{"m"}, BaseWeight: 100}, {ChannelID: 2, Models: []string{"m"}, BaseWeight: 100}}
	stats := map[int64]ProbeSpeedStats{1: {Samples: 5, MeanSeconds: 1, LastStartedAt: now}, 2: {Samples: 5, MeanSeconds: 4, LastStartedAt: now}}
	require.Equal(t, 2.5, probeSpeedBaseline(rows, stats, nil, 5, now))
	require.Zero(t, probeSpeedBaseline(rows, stats, map[int64]ContinuousState{2: {RoutineCircuitLatched: true}}, 5, now))
	for _, sample := range []ProbeSpeedStats{{Samples: 4, MeanSeconds: 1, LastStartedAt: now}, {Samples: 5, MeanSeconds: 1, LastStartedAt: now.Add(-91 * time.Second)}, {Samples: 5, MeanSeconds: math.NaN(), LastStartedAt: now}} {
		got := evaluateProbeSpeed(sample, 2.5, p, now)
		require.False(t, got.Ready)
		require.Equal(t, float64(1), got.Factor)
	}
	got := evaluateProbeSpeed(stats[1], 2.5, p, now)
	require.Equal(t, 1.2, got.Factor)
	state := ContinuousState{KSpeed: 1.1, KCache: .9, KOTPS: 1.05, KError: .8, ProbeSpeed: got}
	require.InDelta(t, 1.1*.9*1.05*.8*1.2, combinedFactor(state, p), 1e-10)
	p.ProbeSpeedExponent = 0
	require.False(t, evaluateProbeSpeed(stats[1], 2.5, p, now).Ready)
	require.InDelta(t, 1.1*.9*1.05*.8, combinedFactor(state, p), 1e-10)
}

func TestProbeSpeedCannotBypassCircuitOrCapacity(t *testing.T) {
	for _, fault := range []bool{false, true} {
		f, _, _, policy := routineFixture()
		policy.Policy.Continuous.MinSamples = 5
		f.bases = append(f.bases, ChannelBaseValue{ChannelID: 2, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 100})
		now := time.Now()
		state := f.states[1]
		if fault {
			state.RoutineCircuitLatched = true
			state.CircuitDisabled = true
			state.CircuitStatusTarget = 2
			state.Phase = "circuit"
		} else {
			f.bases[0].MaxRPM = 1
			f.metrics = []ChannelMetric{{ChannelID: 1, RequestCount: 100}}
		}
		f.states[1] = state
		s := &probeSpeedFake{f, map[int64]ProbeSpeedStats{1: {Samples: 5, MeanSeconds: 1, LastStartedAt: now}, 2: {Samples: 5, MeanSeconds: 4, LastStartedAt: now}}}
		NewEngine(s).evaluateContinuous("i", policy, now, s)
		for _, w := range f.writes {
			if w.ChannelID == 1 {
				require.Zero(t, w.ProposedWeight)
			}
		}
	}
}

func TestProbeSpeedPolicyDefaultsAndDisable(t *testing.T) {
	p, err := DecodePolicyJSON([]byte(`{"continuous":{"min_samples":5}}`))
	require.NoError(t, err)
	require.Equal(t, .25, p.Continuous.ProbeSpeedExponent)
	require.Equal(t, int64(5), p.Continuous.MinSamples)
	p.Continuous.ProbeSpeedExponent = 0
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	round, err := DecodePolicyJSON(raw)
	require.NoError(t, err)
	require.Zero(t, round.Continuous.ProbeSpeedExponent)
	require.Empty(t, round.Validate())
	round.Continuous.ProbeSpeedMinFactor = 0
	require.Contains(t, round.Validate(), "continuous.probe_speed_factor_range")
}

func TestRoutineMeasurementUsesWallTimeAndMarksSupplemental(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var samples []routineResult
	call := 0
	p := routineTestProber{probe: func(context.Context) (channelcontrol.ProbeResult, error) {
		call++
		time.Sleep(5 * time.Millisecond)
		return channelcontrol.ProbeResult{Success: call > 1, Duration: .5}, nil
	}}
	runRoutineChannel(ctx, p, 1, "m", make(chan struct{}, 4), routineTiming{time.Hour, time.Second}, func(routineStreak, time.Time) bool { return true }, func(r routineResult) {
		samples = append(samples, r)
		if len(samples) == 2 {
			cancel()
		}
	})
	require.Len(t, samples, 2)
	require.True(t, samples[0].scheduled)
	require.False(t, samples[1].scheduled)
	require.Greater(t, samples[1].duration, .003)
	require.Less(t, samples[1].duration, .3, "use CT wall clock, not upstream rounded reported duration")
}

func TestRoutineTimeoutRecordsOneCappedSpeedSample(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release := make(chan struct{})
	defer close(release)
	var got []routineResult
	p := routineTestProber{probe: func(context.Context) (channelcontrol.ProbeResult, error) {
		<-release
		return channelcontrol.ProbeResult{Success: true}, nil
	}}
	runRoutineChannel(ctx, p, 1, "m", make(chan struct{}, 4), routineTiming{time.Hour, 20 * time.Millisecond}, func(routineStreak, time.Time) bool { return true }, func(r routineResult) { got = append(got, r); cancel() })
	require.Len(t, got, 1)
	require.Equal(t, routineSlow, got[0].outcome)
	require.Equal(t, .020, got[0].duration)
	require.True(t, got[0].scheduled)
}
