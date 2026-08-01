package tuning

import (
	"math"
	"testing"
	"time"
)

func TestContinuousBaselineRequiresTwoComparableChannels(t *testing.T) {
	rows := []ChannelBaseValue{{ChannelID: 1}, {ChannelID: 2}}
	metrics := map[int64]ChannelMetric{
		1: {ChannelID: 1, RequestCount: 20, TTFTP50: 1, TTFTP90: 2, TTFTP95: 3},
		2: {ChannelID: 2, RequestCount: 19, TTFTP50: 2, TTFTP90: 3, TTFTP95: 4},
	}
	if _, ok := buildContinuousBaseline(rows, metrics, 20); ok {
		t.Fatal("one comparable channel must not produce a relative baseline")
	}
	metrics[2] = ChannelMetric{ChannelID: 2, RequestCount: 20, TTFTP50: 2, TTFTP90: 4, TTFTP95: 6}
	b, ok := buildContinuousBaseline(rows, metrics, 20)
	if !ok || b.ttft50 != 1.5 || b.ttft90 != 3 || b.ttft95 != 4.5 {
		t.Fatalf("unexpected baseline: %#v ok=%v", b, ok)
	}
}

func TestContinuousFactorsRewardFasterChannelAndRespectCap(t *testing.T) {
	b := continuousBaseline{ttft50: 2, ttft90: 4, ttft95: 6}
	fast := ChannelMetric{TTFTP50: 1, TTFTP90: 2, TTFTP95: 3}
	if got := speedFactor(fast, b, 1); got <= 1 {
		t.Fatalf("faster channel should have factor above one, got %v", got)
	}
	if got := continuousRatioFactor(100, 1, 1, 1.5); got != 1.5 {
		t.Fatalf("factor cap not enforced: %v", got)
	}
	if got := continuousRatioFactor(0, 1, 1, 1.5); math.Abs(got-1) > 1e-9 {
		t.Fatalf("missing observation must be neutral: %v", got)
	}
}

type continuousFake struct {
	fakeStore
	bases  []ChannelBaseValue
	states map[int64]ContinuousState
	writes []Recommendation
	probes []Recommendation
}

func (f *continuousFake) CreateContinuousProbe(r Recommendation, _ string, _, _ int, _ time.Time) (string, error) {
	f.probes = append(f.probes, r)
	return "probe-" + r.ID, nil
}

func (f *continuousFake) ListChannelBaseValues(string, string) ([]ChannelBaseValue, error) {
	return f.bases, nil
}
func (f *continuousFake) ListContinuousStates(string) ([]ContinuousState, error) {
	var out []ContinuousState
	for _, s := range f.states {
		out = append(out, s)
	}
	return out, nil
}
func (f *continuousFake) PutContinuousState(s ContinuousState) error {
	if f.states == nil {
		f.states = map[int64]ContinuousState{}
	}
	f.states[s.ChannelID] = s
	return nil
}
func (f *continuousFake) CreateContinuousWeightChange(r Recommendation, _ string, _ time.Time) (string, error) {
	f.writes = append(f.writes, r)
	return "cmd-" + r.ID, nil
}

func autoPolicy() PolicyRecord {
	p := DefaultPolicy()
	p.DispatchModes = map[string]string{"m": "auto"}
	return PolicyRecord{InstanceID: "i", Policy: p, Mode: "observe"}
}

func TestContinuousAutoDedupesAgainstOwnLastWrite(t *testing.T) {
	now := time.Now().UTC()
	f := &continuousFake{bases: []ChannelBaseValue{{
		ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100,
		CurrentWeight: 90, SnapshotAt: now.Add(-time.Hour),
	}}}
	e := NewEngine(f)
	e.evaluateContinuous("i", autoPolicy(), now, f)
	// Snapshot stays stale at 90 for minutes after the write; the identical
	// proposal must not be re-issued every evaluation.
	e.evaluateContinuous("i", autoPolicy(), now.Add(time.Minute), f)
	e.evaluateContinuous("i", autoPolicy(), now.Add(2*time.Minute), f)
	if len(f.writes) != 1 {
		t.Fatalf("stale snapshot must not re-issue identical writes: %d", len(f.writes))
	}
}

func TestManualOverrideOnlyWhenSnapshotPostdatesWrite(t *testing.T) {
	now := time.Now().UTC()
	written := int64(100)
	writeAt := now.Add(-10 * time.Minute)
	stale := &continuousFake{
		bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100,
			CurrentWeight: 90, SnapshotAt: writeAt.Add(-5 * time.Minute)}},
		states: map[int64]ContinuousState{1: {InstanceID: "i", ChannelID: 1, ModelName: "m",
			KError: 1, LastWrittenWeight: &written, LastWriteAt: &writeAt}},
	}
	NewEngine(stale).evaluateContinuous("i", autoPolicy(), now, stale)
	if stale.states[1].PausedReason != "" {
		t.Fatalf("stale snapshot must not flag manual override: %#v", stale.states[1])
	}
	fresh := &continuousFake{
		bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100,
			CurrentWeight: 90, SnapshotAt: writeAt.Add(5 * time.Minute)}},
		states: map[int64]ContinuousState{1: {InstanceID: "i", ChannelID: 1, ModelName: "m",
			KError: 1, LastWrittenWeight: &written, LastWriteAt: &writeAt}},
	}
	NewEngine(fresh).evaluateContinuous("i", autoPolicy(), now, fresh)
	if fresh.states[1].PausedReason != "manual_override" || len(fresh.writes) != 0 {
		t.Fatalf("fresh differing snapshot must pause channel: %#v writes=%d", fresh.states[1], len(fresh.writes))
	}
}

func TestErrorDecayFoldsEachBucketExactlyOnce(t *testing.T) {
	now := time.Now().UTC()
	settledBucket := now.Add(-3 * time.Minute)
	freshBucket := now.Add(-30 * time.Second)
	f := &continuousFake{}
	f.recentBuckets = map[int64][]RecentChannelBucket{1: {
		{BucketTime: freshBucket, RequestCount: 50, ErrorCount: 50},
		{BucketTime: settledBucket, RequestCount: 10, ErrorCount: 4, UserErrorCount: 1},
	}}
	e := NewEngine(f)
	state := ContinuousState{InstanceID: "i", ChannelID: 1, KError: 1}
	e.foldErrorDecay("i", 1, &state, now)
	want := clamp(1*mathPow(.8, 3)*mathPow(1.08, 6), .001, 1)
	if mathAbs(state.KError-want) > 1e-9 {
		t.Fatalf("decay mismatch: got %v want %v (fresh bucket must be excluded)", state.KError, want)
	}
	after := state.KError
	e.foldErrorDecay("i", 1, &state, now)
	if mathAbs(state.KError-after) > 1e-9 {
		t.Fatalf("cursor must prevent re-folding the same bucket: %v -> %v", after, state.KError)
	}
	// Once the fresh bucket settles past the 90s lag it must fold exactly once.
	e.foldErrorDecay("i", 1, &state, now.Add(2*time.Minute))
	if state.KError >= after {
		t.Fatalf("settled bucket must eventually fold: %v", state.KError)
	}
}

func TestMixedChannelFuseSkipsContinuousTuning(t *testing.T) {
	now := time.Now().UTC()
	f := &continuousFake{bases: []ChannelBaseValue{{
		ChannelID: 1, ModelName: "m", Models: []string{"m", "n"}, BaseWeight: 100,
		CurrentWeight: 90, SnapshotAt: now,
	}}}
	e := NewEngine(f)
	e.evaluateContinuous("i", autoPolicy(), now, f)
	e.evaluateContinuous("i", autoPolicy(), now.Add(time.Minute), f)
	if len(f.writes) != 0 || f.states[1].PausedReason != "mixed_channel" || f.states[1].Multiplier != 1 {
		t.Fatalf("mixed channel must be fused off: %#v writes=%d", f.states[1], len(f.writes))
	}
	events := 0
	for _, r := range f.recommendations {
		if r.Rule == "mixed_channel" {
			events++
		}
	}
	if events != 1 {
		t.Fatalf("mixed fuse event must be recorded once: %d", events)
	}
}

func TestContinuousCircuitProbeAndSoftStart(t *testing.T) {
	now := time.Now().UTC()
	p := autoPolicy()
	p.Policy.Continuous.CircuitThreshold = .1
	p.Policy.Continuous.RecoveryThreshold = .2
	p.Policy.Continuous.SilentMinutes = 5
	p.Policy.Continuous.SoftStartMultiplier = .2
	f := &continuousFake{bases: []ChannelBaseValue{
		{ChannelID: 1, ChannelName: "c", ModelName: "m", Models: []string{"m"}, BaseWeight: 100, BasePriority: 7, CurrentWeight: 100, CurrentPriority: 7, SnapshotAt: now},
		{ChannelID: 2, ChannelName: "peer", ModelName: "m", Models: []string{"m"}, BaseWeight: 100, BasePriority: 7, CurrentWeight: 100, CurrentPriority: 7, SnapshotAt: now},
	}, states: map[int64]ContinuousState{1: {InstanceID: "i", ChannelID: 1, ModelName: "m", KError: .05, Phase: "normal"}}}
	f.metrics = []ChannelMetric{{ChannelID: 1, RequestCount: 20, TTFTP50: 1, TTFTP90: 1, TTFTP95: 1}, {ChannelID: 2, RequestCount: 20, TTFTP50: 1, TTFTP90: 1, TTFTP95: 1}}
	e := NewEngine(f)
	e.evaluateContinuous("i", p, now, f)
	s := f.states[1]
	if s.Phase != "circuit" || s.ProposedWeight != 0 || s.NextProbeAt == nil || len(f.writes) != 1 || f.writes[0].ProposedPriority == nil || *f.writes[0].ProposedPriority != 0 {
		t.Fatalf("circuit did not open safely: %#v writes=%#v", s, f.writes)
	}
	e.evaluateContinuous("i", p, now.Add(5*time.Minute), f)
	s = f.states[1]
	if s.Phase != "probing" || s.ProbeCommandID == nil || len(f.probes) != 1 {
		t.Fatalf("probe was not scheduled: %#v", s)
	}
	s.ProbeCommandID = nil
	s.ProbeAttempts = 10
	s.ProbeSuccesses = 10
	s.ProbeDurationSum = 10
	f.states[1] = s
	e.evaluateContinuous("i", p, now.Add(6*time.Minute), f)
	s = f.states[1]
	if s.Phase != "soft_start" || s.ProposedWeight != 20 || !s.SoftStartPending || len(f.writes) != 2 {
		t.Fatalf("successful probe must soft-start: %#v", s)
	}
}

func mathPow(a, b float64) float64 { return math.Pow(a, b) }
func mathAbs(a float64) float64    { return math.Abs(a) }

func TestRecoveryFloorsErrorDecayAgainstReCircuitLoop(t *testing.T) {
	now := time.Now().UTC()
	p := autoPolicy()
	f := &continuousFake{bases: []ChannelBaseValue{
		{ChannelID: 1, ChannelName: "c", ModelName: "m", Models: []string{"m"}, BaseWeight: 100, BasePriority: 7, CurrentWeight: 0, CurrentPriority: 0, SnapshotAt: now},
	}, states: map[int64]ContinuousState{1: {InstanceID: "i", ChannelID: 1, ModelName: "m",
		KError: .01, Phase: "probing", ProbeAttempts: 10, ProbeSuccesses: 10}}}
	e := NewEngine(f)
	// Probe round passed -> soft start.
	e.evaluateContinuous("i", p, now, f)
	if f.states[1].Phase != "soft_start" {
		t.Fatalf("passed probe round must recover: %#v", f.states[1])
	}
	if f.states[1].KError < p.Policy.Continuous.RecoveryThreshold {
		t.Fatalf("recovery must floor the error decay: %v", f.states[1].KError)
	}
	// Soft-start hold cycle, then the first normal cycle: without the floor
	// the crushed KError would re-open the circuit immediately.
	e.evaluateContinuous("i", p, now.Add(time.Minute), f)
	e.evaluateContinuous("i", p, now.Add(2*time.Minute), f)
	if f.states[1].Phase != "normal" {
		t.Fatalf("recovered channel with no new failures must settle to normal, got %q", f.states[1].Phase)
	}
}
