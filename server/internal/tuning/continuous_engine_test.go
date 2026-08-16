package tuning

import (
	"errors"
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

func TestContinuousBaselineUsesArithmeticAverage(t *testing.T) {
	rows := []ChannelBaseValue{{ChannelID: 1}, {ChannelID: 2}, {ChannelID: 3}}
	metrics := map[int64]ChannelMetric{
		1: {ChannelID: 1, RequestCount: 20, TTFTP50: 1, TTFTP90: 2, TTFTP95: 3, CacheHitRate: .1, CachePromptTokens: cacheEvidenceTokens, OTPS: 10, OTPSSampleTokens: otpsEvidenceTokens},
		2: {ChannelID: 2, RequestCount: 20, TTFTP50: 2, TTFTP90: 4, TTFTP95: 6, CacheHitRate: .2, CachePromptTokens: cacheEvidenceTokens, OTPS: 20, OTPSSampleTokens: otpsEvidenceTokens},
		3: {ChannelID: 3, RequestCount: 20, TTFTP50: 9, TTFTP90: 12, TTFTP95: 15, CacheHitRate: .9, CachePromptTokens: cacheEvidenceTokens, OTPS: 90, OTPSSampleTokens: otpsEvidenceTokens},
	}
	b, ok := buildContinuousBaseline(rows, metrics, 20)
	if !ok || !b.cacheReady || !b.otpsReady {
		t.Fatalf("expected complete baseline: %#v ok=%v", b, ok)
	}
	for name, pair := range map[string][2]float64{
		"ttft50": {b.ttft50, 4}, "ttft90": {b.ttft90, 6}, "ttft95": {b.ttft95, 8},
		"cache": {b.cache, .4}, "otps": {b.otps, 40},
	} {
		if math.Abs(pair[0]-pair[1]) > 1e-9 {
			t.Fatalf("%s=%v want arithmetic average %v", name, pair[0], pair[1])
		}
	}
}

func TestContinuousFactorsRewardFasterChannelAndRespectCap(t *testing.T) {
	b := continuousBaseline{ttft50: 2, ttft90: 4, ttft95: 6, cache: .5, otps: 50, cacheReady: true, otpsReady: true}
	fast := ChannelMetric{TTFTP50: 1, TTFTP90: 2, TTFTP95: 3, CacheHitRate: .8, CachePromptTokens: cacheEvidenceTokens, OTPS: 80, OTPSSampleTokens: otpsEvidenceTokens}
	if got := speedFactor(fast, b, 1, .35, .75, 1.25); got <= 1 {
		t.Fatalf("faster channel should have factor above one, got %v", got)
	}
	p := DefaultPolicy().Continuous
	_, cache, otps := performanceFactors(fast, b, p, true, true)
	if cache <= 1 || cache > 1.1 || otps <= 1 || otps > 1.2 {
		t.Fatalf("relative factors must reward better evidence within caps: cache=%v otps=%v", cache, otps)
	}
	_, cache, otps = performanceFactors(fast, b, p, false, false)
	if math.Abs(cache-1) > 1e-9 || math.Abs(otps-1) > 1e-9 {
		t.Fatalf("unavailable optional evidence must be neutral: cache=%v otps=%v", cache, otps)
	}
}

func TestContinuousFactorsUseConfiguredCurves(t *testing.T) {
	p := DefaultPolicy().Continuous
	p.CacheExponent, p.CacheMinFactor, p.CacheMaxFactor = 1, .6, 1.4
	p.OTPSExponent, p.OTPSMinFactor, p.OTPSMaxFactor = 1, .7, 1.6
	b := continuousBaseline{ttft50: 1, ttft90: 1, ttft95: 1, cache: .5, otps: 10}
	m := ChannelMetric{TTFTP50: 1, TTFTP90: 1, TTFTP95: 1, CacheHitRate: .25, OTPS: 20}
	_, cache, otps := performanceFactors(m, b, p, true, true)
	if cache != .6 || otps != 1.6 {
		t.Fatalf("configured factor bounds were ignored: cache=%v otps=%v", cache, otps)
	}
	p.ErrorHealthyRate, p.ErrorDegradedRate, p.ErrorPoorRate, p.ErrorFloorRate = .02, .10, .20, .40
	p.ErrorDegradedFactor, p.ErrorPoorFactor, p.ErrorMinFactor = .9, .6, .3
	if got := reliabilityFactorWithPolicy(.10, p); math.Abs(got-.9) > 1e-9 {
		t.Fatalf("configured error curve was ignored: %v", got)
	}
	p.CombinedMinFactor, p.CombinedMaxFactor = .8, 1.1
	if got := combinedFactor(ContinuousState{KSpeed: 2, KCache: 1, KOTPS: 1, KError: 1}, p); got != 1.1 {
		t.Fatalf("configured combined bound was ignored: %v", got)
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
	actual := f.states[1].ProposedWeight
	shifted := f.states[1]
	anchor := actual - 4
	shifted.LastObservedWeight = &anchor
	f.states[1] = shifted
	e.evaluateContinuous("i", PolicyRecord{InstanceID: "i", Policy: p, Mode: "observe"}, now.Add(2*time.Minute), f)
	if len(f.recommendations) != 2 {
		t.Fatalf("proposal changes inside the deadband must not spam history: %d", len(f.recommendations))
	}
	shifted = f.states[1]
	anchorFar := actual - 6
	shifted.LastObservedWeight = &anchorFar
	f.states[1] = shifted
	e.evaluateContinuous("i", PolicyRecord{InstanceID: "i", Policy: p, Mode: "observe"}, now.Add(3*time.Minute), f)
	if len(f.recommendations) != 3 || f.recommendations[2].ChannelID != 1 {
		t.Fatalf("proposal changes outside the deadband must be observed: %#v", f.recommendations)
	}
	if got := f.states[1].LastObservedWeight; got == nil || *got != actual {
		t.Fatalf("recording an event must move the anchor to the recorded value: %#v", got)
	}
}

// A creeping drift must eventually be recorded: the deadband anchors on the
// last RECORDED event, not on the previous tick, so small per-tick steps
// accumulate against the anchor instead of resetting the reference.
func TestObserveDeadbandAnchorsOnLastRecordedEvent(t *testing.T) {
	now := time.Now().UTC()
	anchor := int64(100)
	f := &continuousFake{
		bases: []ChannelBaseValue{
			{ChannelID: 1, ChannelName: "drift", ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 100},
			{ChannelID: 2, ChannelName: "peer", ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 100},
		},
		states: map[int64]ContinuousState{
			1: {InstanceID: "i", ChannelID: 1, ModelName: "m", KError: 1, LastObservedWeight: &anchor},
			2: {InstanceID: "i", ChannelID: 2, ModelName: "m", KError: 1, LastObservedWeight: &anchor},
		},
	}
	p := DefaultPolicy()
	p.DispatchModes = map[string]string{"m": "observe"}
	e := NewEngine(f)
	// Channel 1 drifts 2 units per tick via a slowly-degrading error factor
	// stand-in: simulate by shrinking KError before each pass.
	for i, kerr := range []float64{.98, .96, .94} {
		st := f.states[1]
		st.KError = kerr
		f.states[1] = st
		f.metrics = []ChannelMetric{
			{ChannelID: 1, RequestCount: 30, TTFTP50: 1, TTFTP90: 1, TTFTP95: 1},
			{ChannelID: 2, RequestCount: 30, TTFTP50: 1, TTFTP90: 1, TTFTP95: 1},
		}
		e.evaluateContinuous("i", PolicyRecord{InstanceID: "i", Policy: p, Mode: "observe"}, now.Add(time.Duration(i)*time.Minute), f)
	}
	var drifted int
	for _, r := range f.recommendations {
		if r.Rule == "weight_observed" && r.ChannelID == 1 {
			drifted++
		}
	}
	if drifted != 1 {
		t.Fatalf("cumulative drift beyond the deadband must be recorded exactly once: %d events %#v", drifted, f.recommendations)
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
	writeErr        error
	writeAttempts   int
	metricsQueries  int
}

func (f *continuousFake) GetPolicy(string) (PolicyRecord, bool, error) {
	return PolicyRecord{}, false, nil
}
func (f *continuousFake) PutPolicy(PolicyRecord) error        { return nil }
func (f *continuousFake) ListEnabledSites() ([]string, error) { return nil, nil }
func (f *continuousFake) QueryMetrics(string, time.Time, time.Time) ([]ChannelMetric, error) {
	f.metricsQueries++
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
	f.writeAttempts++
	if f.writeErr != nil {
		return "", f.writeErr
	}
	f.writes = append(f.writes, r)
	return "cmd-" + r.ID, nil
}

func autoPolicy() PolicyRecord {
	p := DefaultPolicy()
	p.DispatchModes = map[string]string{"m": "auto"}
	return PolicyRecord{InstanceID: "i", Policy: p, Mode: "observe"}
}

func TestContinuousSkipsMetricsWhenEveryModelIsOff(t *testing.T) {
	p := DefaultPolicy()
	p.DispatchModes = map[string]string{"m": "off"}
	f := &continuousFake{bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", BaseWeight: 100}}}

	writes, evaluated := NewEngine(f).evaluateContinuous("i", PolicyRecord{InstanceID: "i", Policy: p}, time.Now().UTC(), f)

	if writes != 0 || evaluated != 0 || f.metricsQueries != 0 {
		t.Fatalf("off-only site must do no metric work: writes=%d evaluated=%d metric_queries=%d", writes, evaluated, f.metricsQueries)
	}
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

func TestContinuousAutoWritesEveryIntegerTargetChange(t *testing.T) {
	now := time.Now().UTC()
	writeAt := now.Add(-time.Minute)
	last := int64(99)
	f := &continuousFake{
		bases:  []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: last, SnapshotAt: now.Add(-time.Hour)}},
		states: map[int64]ContinuousState{1: {InstanceID: "i", ChannelID: 1, ModelName: "m", KError: 1, LastWrittenWeight: &last, LastWriteAt: &writeAt}},
	}
	pr := autoPolicy()
	NewEngine(f).evaluateContinuous("i", pr, now, f)
	if len(f.writes) != 1 || f.writes[0].ProposedWeight != 100 {
		t.Fatalf("one-unit target change must write immediately: %#v", f.writes)
	}
}

func TestContinuousAutoDoesNotWaitForMinimumWriteInterval(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name       string
		lastWrite  *time.Time
		wantWrites int
	}{
		{name: "inside interval", lastWrite: timePointer(now.Add(-4 * time.Minute)), wantWrites: 1},
		{name: "at interval", lastWrite: timePointer(now.Add(-5 * time.Minute)), wantWrites: 1},
		{name: "first write", lastWrite: nil, wantWrites: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			last := int64(90)
			state := ContinuousState{InstanceID: "i", ChannelID: 1, ModelName: "m", KError: 1, LastWriteAt: tc.lastWrite}
			if tc.lastWrite != nil {
				state.LastWrittenWeight = &last
			}
			f := &continuousFake{bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 90, SnapshotAt: now.Add(-time.Hour)}}, states: map[int64]ContinuousState{1: state}}
			NewEngine(f).evaluateContinuous("i", autoPolicy(), now, f)
			if len(f.writes) != tc.wantWrites {
				t.Fatalf("writes=%d want=%d state=%#v", len(f.writes), tc.wantWrites, f.states[1])
			}
		})
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func TestAutoReassertsCalculatedWeightAfterConfirmedExternalChange(t *testing.T) {
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
	if stale.states[1].PausedReason != "" || len(stale.writes) != 0 {
		t.Fatalf("stale snapshot must neither pause nor duplicate: state=%#v writes=%d", stale.states[1], len(stale.writes))
	}
	fresh := &continuousFake{
		bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100,
			CurrentWeight: 90, SnapshotAt: writeAt.Add(5 * time.Minute)}},
		states: map[int64]ContinuousState{1: {InstanceID: "i", ChannelID: 1, ModelName: "m",
			KError: 1, LastWrittenWeight: &written, LastWriteAt: &writeAt}},
	}
	NewEngine(fresh).evaluateContinuous("i", autoPolicy(), now, fresh)
	if fresh.states[1].PausedReason != "" || len(fresh.writes) != 1 || fresh.writes[0].ProposedWeight != 100 {
		t.Fatalf("fresh external change must be overwritten by auto: %#v writes=%#v", fresh.states[1], fresh.writes)
	}
}

func TestLegacyManualOverrideIsClearedBeforeCircuitEarlyReturn(t *testing.T) {
	now := time.Now().UTC()
	nextProbe := now.Add(5 * time.Minute)
	f := &continuousFake{
		bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 0, SnapshotAt: now}},
		states: map[int64]ContinuousState{1: {
			InstanceID: "i", ChannelID: 1, ModelName: "m", KError: .2,
			PausedReason: "manual_override", Phase: "circuit", NextProbeAt: &nextProbe,
		}},
	}
	NewEngine(f).evaluateContinuous("i", autoPolicy(), now, f)
	if got := f.states[1].PausedReason; got != "" {
		t.Fatalf("legacy manual override must clear even while circuit branch returns early: %q", got)
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
	lastWritten, lastWriteAt := int64(100), now
	f := &continuousFake{bases: []ChannelBaseValue{
		{ChannelID: 1, ChannelName: "c", ModelName: "m", Models: []string{"m"}, BaseWeight: 100, BasePriority: 7, CurrentWeight: 100, CurrentPriority: 7, SnapshotAt: now},
		{ChannelID: 2, ChannelName: "peer", ModelName: "m", Models: []string{"m"}, BaseWeight: 100, BasePriority: 7, CurrentWeight: 100, CurrentPriority: 7, SnapshotAt: now},
	}, states: map[int64]ContinuousState{1: {InstanceID: "i", ChannelID: 1, ModelName: "m", KError: .2, SmoothedErrorRate: .35, Phase: "normal", LastWrittenWeight: &lastWritten, LastWriteAt: &lastWriteAt}}}
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
			// Below the normal 20-request performance threshold: these requests
			// are still sufficient as a 10-request passive recovery round.
			{ChannelID: 1, RequestCount: 10},
			{ChannelID: 2, RequestCount: 30, TTFTP50: 1, TTFTP90: 1, TTFTP95: 1, CacheHitRate: .5, OTPS: 10},
		},
		states: map[int64]ContinuousState{
			// A faithful v2 circuit state carries the smoothed rate that
			// tripped or sustained it; a zero rate with decayed KError is the
			// v1-legacy shape and triggers the upgrade seeding instead.
			1: {InstanceID: "i", ChannelID: 1, ModelName: "m", KError: .605, SmoothedErrorRate: .12, Phase: "circuit", ProposedWeight: 0},
		},
		recentBuckets: map[int64][]RecentChannelBucket{
			1: {{BucketTime: settled, RequestCount: 10}},
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

func TestObservedCircuitWaitsForPassiveRecoveryEvidence(t *testing.T) {
	now := time.Now().UTC()
	p := DefaultPolicy()
	p.DispatchModes = map[string]string{"m": "observe"}
	f := &continuousFake{
		bases:   []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100}},
		metrics: []ChannelMetric{{ChannelID: 1, RequestCount: 9}},
		states: map[int64]ContinuousState{1: {
			InstanceID: "i", ChannelID: 1, ModelName: "m", KError: .2,
			SmoothedErrorRate: .3, Phase: "circuit",
		}},
		recentBuckets: map[int64][]RecentChannelBucket{1: {{
			BucketTime: now.Add(-3 * time.Minute), RequestCount: 9,
		}}},
	}

	NewEngine(f).evaluateContinuous("i", PolicyRecord{InstanceID: "i", Policy: p, Mode: "observe"}, now, f)

	state := f.states[1]
	if state.Phase != "circuit" || state.ProbeAttempts != 9 {
		t.Fatalf("passive recovery must wait for its own evidence threshold: %#v", state)
	}
}

func TestZeroBaseWeightDoesNotCircuitOrRecover(t *testing.T) {
	now := time.Now().UTC()
	due := now.Add(-time.Minute)
	p := DefaultPolicy()
	p.DispatchModes = map[string]string{"m": "observe"}
	f := &continuousFake{
		bases:   []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 0, CurrentWeight: 20}},
		metrics: []ChannelMetric{{ChannelID: 1, RequestCount: 100, ErrorCount: 100, TTFTP50: 1, TTFTP90: 1, TTFTP95: 1}},
		states: map[int64]ContinuousState{1: {
			InstanceID: "i", ChannelID: 1, ModelName: "m", KError: .2,
			SmoothedErrorRate: .3, Phase: "circuit", NextProbeAt: &due,
			ProbeAttempts: 10, ProbeSuccesses: 10,
		}},
	}

	NewEngine(f).evaluateContinuous("i", PolicyRecord{InstanceID: "i", Policy: p, Mode: "observe"}, now, f)

	state := f.states[1]
	if state.Phase != "normal" || state.ProposedWeight != 0 || state.ProbeAttempts != 0 {
		t.Fatalf("zero base weight must be excluded from circuit state machine: %#v", state)
	}
	if len(f.recommendations) != 0 || len(f.probes) != 0 || len(f.writes) != 0 {
		t.Fatalf("zero base weight must not emit tuning actions: recommendations=%d probes=%d writes=%d", len(f.recommendations), len(f.probes), len(f.writes))
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

// Probe counters are shared between observe-mode passive accumulation and
// auto-mode active rounds. Switching a channel to auto with leftover passive
// counts must start the active probe round from zero.
func TestActiveProbeRoundDiscardsPassiveResidue(t *testing.T) {
	now := time.Now().UTC()
	due := now.Add(-time.Minute)
	p := DefaultPolicy()
	p.DispatchModes = map[string]string{"m": "auto"}
	f := &continuousFake{
		bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100}},
		states: map[int64]ContinuousState{1: {
			InstanceID: "i", ChannelID: 1, ModelName: "m", KError: .2,
			SmoothedErrorRate: .3, Phase: "circuit", NextProbeAt: &due,
			ProbeAttempts: 7, ProbeSuccesses: 7, ProbeDurationSum: 3,
		}},
	}

	NewEngine(f).evaluateContinuous("i", PolicyRecord{InstanceID: "i", Policy: p, Mode: "auto"}, now, f)

	state := f.states[1]
	if len(f.probes) != 1 || state.Phase != "probing" {
		t.Fatalf("due circuit must start an active probe: probes=%d %#v", len(f.probes), state)
	}
	if state.ProbeAttempts != 0 || state.ProbeSuccesses != 0 || state.ProbeDurationSum != 0 {
		t.Fatalf("active round must discard passive residue: %#v", state)
	}
}

func TestWriteFailureStreakPausesThenSelfHeals(t *testing.T) {
	now := time.Now().UTC()
	f := &continuousFake{
		bases:    []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 10, CurrentWeight: 20, SnapshotAt: now.Add(-time.Hour)}},
		writeErr: errors.New("new-api channel update failed: unauthorized"),
	}
	e := NewEngine(f)
	pr := autoPolicy()

	for i := 0; i < 3; i++ {
		e.evaluateContinuous("i", pr, now.Add(time.Duration(i)*time.Minute), f)
	}
	state := f.states[1]
	if f.writeAttempts != 3 || state.PausedReason != "write_failed" || state.WriteFailureStreak != 3 {
		t.Fatalf("three failures must pause: attempts=%d state=%#v", f.writeAttempts, state)
	}
	paused := 0
	for _, r := range f.recommendations {
		if r.Rule == "auto_paused" {
			paused++
			if r.Evidence["reason"] != "write_failed" || r.Evidence["error"] == "" {
				t.Fatalf("pause event must carry the transport error: %#v", r.Evidence)
			}
		}
	}
	if paused != 1 {
		t.Fatalf("exactly one pause event expected, got %d", paused)
	}

	// Within the retry interval the engine must stop hammering new-api.
	e.evaluateContinuous("i", pr, now.Add(4*time.Minute), f)
	if f.writeAttempts != 3 {
		t.Fatalf("paused channel must not retry inside the interval: attempts=%d", f.writeAttempts)
	}

	// After the interval one retry runs; on success the pause self-heals.
	f.writeErr = nil
	lastWritten, lastWriteAt := int64(20), now.Add(2*time.Minute+writeFailureRetryInterval)
	state = f.states[1]
	state.LastWrittenWeight, state.LastWriteAt = &lastWritten, &lastWriteAt
	f.states[1] = state
	e.evaluateContinuous("i", pr, lastWriteAt, f)
	state = f.states[1]
	if f.writeAttempts != 4 || state.PausedReason != "" || state.WriteFailureStreak != 0 || state.LastWriteError != "" {
		t.Fatalf("successful retry must clear the pause: attempts=%d state=%#v", f.writeAttempts, state)
	}
	if state.LastWrittenWeight == nil || *state.LastWrittenWeight != 10 {
		t.Fatalf("recovered write must land: %#v", state.LastWrittenWeight)
	}
}

func TestWriteFailurePauseClearsWhenDueChangeEvaporates(t *testing.T) {
	now := time.Now().UTC()
	failedAt := now.Add(-writeFailureRetryInterval)
	lastWriteAt := now.Add(-time.Minute)
	for _, tc := range []struct {
		name    string
		current int64
		last    int64
	}{
		{name: "already written", current: 100, last: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			last := tc.last
			f := &continuousFake{
				bases:  []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: tc.current, SnapshotAt: now.Add(-time.Hour)}},
				states: map[int64]ContinuousState{1: {InstanceID: "i", ChannelID: 1, ModelName: "m", KError: 1, LastWrittenWeight: &last, LastWriteAt: &lastWriteAt, PausedReason: "write_failed", WriteFailureStreak: 3, LastWriteFailureAt: &failedAt, LastWriteError: "boom"}},
			}
			NewEngine(f).evaluateContinuous("i", autoPolicy(), now, f)
			state := f.states[1]
			if f.writeAttempts != 0 || state.PausedReason != "" || state.WriteFailureStreak != 0 || state.LastWriteError != "" {
				t.Fatalf("evaporated change must clear pause without writing: attempts=%d state=%#v", f.writeAttempts, state)
			}
		})
	}
}

func TestWriteFailurePauseKeepsRetryFailuresQuiet(t *testing.T) {
	now := time.Now().UTC()
	f := &continuousFake{
		bases:    []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 10, CurrentWeight: 20, SnapshotAt: now.Add(-time.Hour)}},
		writeErr: errors.New("boom"),
	}
	e := NewEngine(f)
	for i := 0; i < 3; i++ {
		e.evaluateContinuous("i", autoPolicy(), now.Add(time.Duration(i)*time.Minute), f)
	}
	// A failing slow retry must extend the pause without a second event.
	e.evaluateContinuous("i", autoPolicy(), now.Add(2*time.Minute+writeFailureRetryInterval), f)
	if f.writeAttempts != 4 {
		t.Fatalf("slow retry expected: attempts=%d", f.writeAttempts)
	}
	paused := 0
	for _, r := range f.recommendations {
		if r.Rule == "auto_paused" {
			paused++
		}
	}
	if paused != 1 || f.states[1].PausedReason != "write_failed" || f.states[1].WriteFailureStreak != 4 {
		t.Fatalf("failed retry must stay paused without duplicate events: paused=%d state=%#v", paused, f.states[1])
	}
}

func TestWriteFailedPauseClearsWhenWritesCannotRun(t *testing.T) {
	now := time.Now().UTC()
	failedAt := now.Add(-time.Minute)
	seed := func() map[int64]ContinuousState {
		return map[int64]ContinuousState{1: {InstanceID: "i", ChannelID: 1, ModelName: "m", KError: 1,
			PausedReason: "write_failed", WriteFailureStreak: 3, LastWriteError: "boom", LastWriteFailureAt: &failedAt}}
	}

	// Observe mode cannot write, so the stale pause must clear.
	observe := &continuousFake{
		bases:  []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 10, CurrentWeight: 20, SnapshotAt: now.Add(-time.Hour)}},
		states: seed(),
	}
	p := DefaultPolicy()
	p.DispatchModes = map[string]string{"m": "observe"}
	NewEngine(observe).evaluateContinuous("i", PolicyRecord{InstanceID: "i", Policy: p, Mode: "observe"}, now, observe)
	if s := observe.states[1]; s.PausedReason != "" || s.WriteFailureStreak != 0 || s.LastWriteError != "" {
		t.Fatalf("observe mode must clear a stale write_failed pause: %#v", s)
	}

	// Zero base weight cannot write either.
	zero := &continuousFake{
		bases:  []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 0, CurrentWeight: 20, SnapshotAt: now.Add(-time.Hour)}},
		states: seed(),
	}
	NewEngine(zero).evaluateContinuous("i", autoPolicy(), now, zero)
	if s := zero.states[1]; s.PausedReason != "" || s.WriteFailureStreak != 0 {
		t.Fatalf("zero base weight must clear a stale write_failed pause: %#v", s)
	}

	// Auto with weight keeps the pause (and its slow retry) untouched.
	auto := &continuousFake{
		bases:    []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 10, CurrentWeight: 20, SnapshotAt: now.Add(-time.Hour)}},
		states:   seed(),
		writeErr: errors.New("boom"),
	}
	NewEngine(auto).evaluateContinuous("i", autoPolicy(), now, auto)
	if s := auto.states[1]; s.PausedReason != "write_failed" {
		t.Fatalf("auto mode must keep the pause: %#v", s)
	}
}

func TestPausedAutoCircuitWaitsForRealZeroingWrite(t *testing.T) {
	now := time.Now().UTC()
	failedAt := now.Add(-time.Minute)
	f := &continuousFake{
		bases:   []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 10, CurrentWeight: 10, SnapshotAt: now.Add(-time.Hour)}},
		metrics: []ChannelMetric{{ChannelID: 1, RequestCount: 100, ErrorCount: 60, TTFTP50: 1, TTFTP90: 1, TTFTP95: 1}},
		states: map[int64]ContinuousState{1: {InstanceID: "i", ChannelID: 1, ModelName: "m", KError: .2, SmoothedErrorRate: .5, Phase: "normal",
			PausedReason: "write_failed", WriteFailureStreak: 3, LastWriteError: "boom", LastWriteFailureAt: &failedAt}},
		writeErr: errors.New("boom"),
	}
	e := NewEngine(f)

	// Inside the retry backoff: the circuit must NOT open without the write —
	// CT showing "circuit" while new-api serves full weight is the exact
	// inconsistency this guards against.
	e.evaluateContinuous("i", autoPolicy(), now, f)
	s := f.states[1]
	if s.Phase != "normal" || s.NextProbeAt != nil || f.writeAttempts != 0 {
		t.Fatalf("paused auto channel must not enter circuit without writing: %#v attempts=%d", s, f.writeAttempts)
	}
	for _, r := range f.recommendations {
		if r.Rule == "circuit_opened" {
			t.Fatalf("no circuit_opened event may exist without the write: %#v", r)
		}
	}

	// Retry window: the real zeroing write is attempted; failure keeps the
	// channel un-circuited and extends the backoff.
	e.evaluateContinuous("i", autoPolicy(), now.Add(writeFailureRetryInterval), f)
	s = f.states[1]
	if f.writeAttempts != 1 || s.Phase != "normal" || s.NextProbeAt != nil {
		t.Fatalf("failed zeroing write must keep the channel un-circuited: %#v attempts=%d", s, f.writeAttempts)
	}

	// Control path healed: the zeroing write lands, only then the circuit commits.
	f.writeErr = nil
	e.evaluateContinuous("i", autoPolicy(), now.Add(2*writeFailureRetryInterval+time.Minute), f)
	s = f.states[1]
	if f.writeAttempts != 2 || s.Phase != "circuit" || s.LastWrittenWeight == nil || *s.LastWrittenWeight != 0 || s.PausedReason != "" {
		t.Fatalf("successful zeroing write must commit the circuit and clear the pause: %#v attempts=%d", s, f.writeAttempts)
	}
	if len(f.writes) != 1 || f.writes[0].Rule != "circuit_opened" {
		t.Fatalf("the committed write must be the circuit zeroing: %#v", f.writes)
	}
}
