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
	b := continuousBaseline{ttft50: 2, ttft90: 4, ttft95: 6, cache: .5, otps: 50, cacheReady: true, otpsReady: true}
	fast := ChannelMetric{TTFTP50: 1, TTFTP90: 2, TTFTP95: 3, CacheHitRate: .8, CachePromptTokens: cacheEvidenceTokens, OTPS: 80, OTPSSampleTokens: otpsEvidenceTokens}
	if got := speedFactor(fast, b, 1); got <= 1 {
		t.Fatalf("faster channel should have factor above one, got %v", got)
	}
	_, cache, otps := performanceFactors(fast, b, 1, 1.5, true, true)
	if cache <= 1 || cache > 1.1 || otps <= 1 || otps > 1.2 {
		t.Fatalf("relative factors must reward better evidence within caps: cache=%v otps=%v", cache, otps)
	}
	_, cache, otps = performanceFactors(fast, b, 1, 1.5, false, false)
	if math.Abs(cache-1) > 1e-9 || math.Abs(otps-1) > 1e-9 {
		t.Fatalf("unavailable optional evidence must be neutral: cache=%v otps=%v", cache, otps)
	}
}

func TestObservePublishesWindowProgressAndOnlyChangedProposals(t *testing.T) {
	now := time.Now().UTC()
	f := &continuousFake{
		bases: []ChannelBaseValue{
			{ChannelID: 1, ChannelName: "fast", ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 100},
			{ChannelID: 2, ChannelName: "slow", ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 100},
		},
		metrics: []ChannelMetric{
			{ChannelID: 1, RequestCount: 30, ErrorCount: 3, UserErrorCount: 1, TTFTP50: 1, TTFTP90: 1, TTFTP95: 1, CacheHitRate: .5, OTPS: 10},
			{ChannelID: 2, RequestCount: 25, TTFTP50: 2, TTFTP90: 2, TTFTP95: 2, CacheHitRate: .5, OTPS: 10},
		},
	}
	p := DefaultPolicy()
	p.DispatchModes = map[string]string{"m": "observe"}
	e := NewEngine(f)
	e.evaluateContinuous("i", PolicyRecord{InstanceID: "i", Policy: p, Mode: "observe"}, now, f)

	if got := f.states[1]; got.LastObservedRequests != 30 || got.LastObservedErrors != 2 || !got.MetricReady || !got.BaselineReady {
		t.Fatalf("window visibility was not persisted: %#v", got)
	}
	if len(f.recommendations) != 2 || f.recommendations[0].Rule != "weight_observed" {
		t.Fatalf("expected initial observation evidence, got %#v", f.recommendations)
	}
	e.evaluateContinuous("i", PolicyRecord{InstanceID: "i", Policy: p, Mode: "observe"}, now.Add(time.Minute), f)
	if len(f.recommendations) != 2 {
		t.Fatalf("unchanged proposals must not spam observation history: %d", len(f.recommendations))
	}
}

type continuousFake struct {
	metrics         []ChannelMetric
	recentBuckets   map[int64][]RecentChannelBucket
	recommendations []Recommendation
	bases           []ChannelBaseValue
	states          map[int64]ContinuousState
	writes          []Recommendation
	probes          []Recommendation
}

func (f *continuousFake) GetPolicy(string) (PolicyRecord, bool, error) {
	return PolicyRecord{}, false, nil
}
func (f *continuousFake) PutPolicy(PolicyRecord) error        { return nil }
func (f *continuousFake) ListEnabledSites() ([]string, error) { return nil, nil }
func (f *continuousFake) QueryMetrics(string, time.Time, time.Time) ([]ChannelMetric, error) {
	return f.metrics, nil
}
func (f *continuousFake) QueryRecentChannelBuckets(_ string, id int64, _ time.Time, _ int) ([]RecentChannelBucket, error) {
	return f.recentBuckets[id], nil
}
func (f *continuousFake) InsertRecommendation(r Recommendation) error {
	f.recommendations = append(f.recommendations, r)
	return nil
}
func (f *continuousFake) ListRecommendations(RecommendationQuery) ([]Recommendation, error) {
	return f.recommendations, nil
}
func (f *continuousFake) RecommendationReport(RecommendationQuery) (Report, error) {
	return Report{}, nil
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
	want := reliabilityFactor(.3 * 3 / 10)
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
	}, states: map[int64]ContinuousState{1: {InstanceID: "i", ChannelID: 1, ModelName: "m", KError: .2, SmoothedErrorRate: .35, Phase: "normal"}}}
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

func TestObservedCircuitRecoversFromPassiveProductionTraffic(t *testing.T) {
	now := time.Now().UTC()
	settled := now.Add(-3 * time.Minute)
	p := DefaultPolicy()
	p.DispatchModes = map[string]string{"m": "observe"}
	f := &continuousFake{
		bases: []ChannelBaseValue{
			{ChannelID: 1, ChannelName: "recovering", ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 100},
			{ChannelID: 2, ChannelName: "peer", ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 100},
		},
		metrics: []ChannelMetric{
			{ChannelID: 1, RequestCount: 30, TTFTP50: 1, TTFTP90: 1, TTFTP95: 1, CacheHitRate: .5, OTPS: 10},
			{ChannelID: 2, RequestCount: 30, TTFTP50: 1, TTFTP90: 1, TTFTP95: 1, CacheHitRate: .5, OTPS: 10},
		},
		states: map[int64]ContinuousState{
			// A faithful v2 circuit state carries the smoothed rate that
			// tripped or sustained it; a zero rate with decayed KError is the
			// v1-legacy shape and triggers the upgrade seeding instead.
			1: {InstanceID: "i", ChannelID: 1, ModelName: "m", KError: .605, SmoothedErrorRate: .12, Phase: "circuit", ProposedWeight: 0},
		},
		recentBuckets: map[int64][]RecentChannelBucket{
			1: {{BucketTime: settled, RequestCount: 20}},
		},
	}

	NewEngine(f).evaluateContinuous("i", PolicyRecord{InstanceID: "i", Policy: p, Mode: "observe"}, now, f)

	state := f.states[1]
	wantWeight := int64(math.Round(100 * state.KError))
	if state.Phase != "normal" || state.ProposedWeight != wantWeight || state.KError < p.Continuous.RecoveryThreshold {
		t.Fatalf("passive production traffic must recover observed circuit: %#v", state)
	}
	if len(f.probes) != 0 || len(f.writes) != 0 {
		t.Fatalf("observe recovery must not probe or write: probes=%d writes=%d", len(f.probes), len(f.writes))
	}
	found := false
	for _, rec := range f.recommendations {
		if rec.Rule == "circuit_recovered" && rec.ModeAtCreation == "observe" {
			found = true
		}
	}
	if !found {
		t.Fatal("observe recovery event was not recorded")
	}
}

func mathAbs(a float64) float64 { return math.Abs(a) }

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

// A v1 state carries a decayed KError with no smoothed rate. Upgrading must
// seed the rate by inverting the reliability mapping so error memory
// survives, and the inversion must round-trip the mapping.
func TestLegacyErrorRateRoundTripsReliabilityFactor(t *testing.T) {
	for _, factor := range []float64{.95, .85, .7, .5, .3, .2} {
		rate := legacyErrorRate(factor)
		if got := reliabilityFactor(rate); math.Abs(got-factor) > 1e-9 {
			t.Fatalf("round trip factor %v -> rate %v -> %v", factor, rate, got)
		}
	}
	if legacyErrorRate(.1) != .30 {
		t.Fatalf("deep-decayed legacy state must seed at the circuit rate")
	}
}
