package tuning

import (
	"errors"
	"testing"
	"time"
)

func TestUncappedWeightsWaitForActualAcknowledgement(t *testing.T) {
	f := &continuousFake{bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 50}}, states: map[int64]ContinuousState{}, commandStatus: "pending"}
	addNeutralPerformanceEvidence(f)
	e, now := NewEngine(f), time.Now().UTC()
	for i := 0; i < 3; i++ {
		e.evaluateContinuous("i", autoPolicy(), now.Add(time.Duration(i)*30*time.Second), f)
		s := f.states[1]
		if len(f.writes) != 1 || s.LastWrittenWeight != nil || s.LastWriteAt != nil || s.ProposedWeight != 55 || s.Capacity.ConfirmedWeight != 50 {
			t.Fatalf("enqueue must not advance confirmed weight: %+v", s)
		}
	}
	s := f.states[1]
	f.commandStatus = "failed"
	e.settleCapacityWrite(f, "i", f.bases[0], &s, now.Add(2*time.Minute))
	if s.WriteFailureStreak != 1 || s.Capacity.PendingCommandID != "" || s.LastWrittenWeight != nil {
		t.Fatalf("failure was not settled: %+v", s)
	}
	f.states[1] = s
	f.commandStatus = "pending"
	e.evaluateContinuous("i", autoPolicy(), now.Add(3*time.Minute), f)
	s = f.states[1]
	f.commandStatus = "succeeded"
	e.settleCapacityWrite(f, "i", f.bases[0], &s, now.Add(4*time.Minute))
	if s.LastWrittenWeight == nil || *s.LastWrittenWeight != 55 || s.WriteFailureStreak != 0 || s.CapacityLimited {
		t.Fatalf("uncapped receipt must confirm without capacity cooldown: %+v", s)
	}
}

type bucketFailureStore struct{ *continuousFake }

func (f *bucketFailureStore) QueryRecentChannelBucketsBySite(string, time.Time) (map[int64][]RecentChannelBucket, error) {
	return nil, errors.New("bucket batch read failed")
}

func (f *bucketFailureStore) QueryRecentChannelBuckets(string, int64, time.Time, int) ([]RecentChannelBucket, error) {
	return nil, errors.New("bucket read failed")
}

func TestBucketFailureCannotCreatePassiveRecoveryEvidence(t *testing.T) {
	f := &continuousFake{bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 100}}, states: map[int64]ContinuousState{1: {ChannelID: 1, Phase: "circuit", ModelName: "m", SmoothedErrorRate: .5, KError: .2}}}
	addNeutralPerformanceEvidence(f)
	broken, pr := &bucketFailureStore{f}, autoPolicy()
	pr.Policy.DispatchModes["m"] = "observe"
	NewEngine(broken).evaluateContinuous("i", pr, time.Now().UTC(), broken)
	s := f.states[1]
	if s.Phase != "circuit" || s.SmoothedErrorRate != .5 || s.ProbeAttempts != 0 || s.LastBucketAt != nil {
		t.Fatalf("failed query manufactured recovery: %+v", s)
	}
}

func TestCacheBaselineIndependentOfTTFT(t *testing.T) {
	p := DefaultPolicy().Continuous
	rows := []ChannelBaseValue{{ChannelID: 1}, {ChannelID: 2}}
	metrics := map[int64]ChannelMetric{1: {RequestCount: 100, CachePromptTokens: 20000, CacheHitRate: .9}, 2: {RequestCount: 100, CachePromptTokens: 20000, CacheHitRate: .1}}
	b, _ := buildContinuousBaseline(rows, metrics, p.MinSamples)
	_, without, _ := performanceFactors(metrics[1], b, p, b.cacheReady, false)
	for id, m := range metrics {
		m.TTFTP50, m.TTFTP90, m.TTFTP95 = 1, 1, 1
		metrics[id] = m
	}
	b, _ = buildContinuousBaseline(rows, metrics, p.MinSamples)
	_, with, _ := performanceFactors(metrics[1], b, p, b.cacheReady, false)
	if without <= 1 || with != without || b.cache != .5 {
		t.Fatalf("cache changed with unrelated TTFT: %v %v %+v", without, with, b)
	}
}

func TestEvaluationKeepsItsOriginalPolicyAndBase(t *testing.T) {
	f := &continuousFake{bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 100}}, states: map[int64]ContinuousState{}}
	addNeutralPerformanceEvidence(f)
	pr, now := autoPolicy(), time.Now().UTC()
	NewEngine(f).evaluateContinuous("i", pr, now, f)
	evaluation := f.states[1].Evaluation
	if evaluation == nil || evaluation.BaseWeight != 100 || evaluation.Params != pr.Policy.Continuous || evaluation.EvaluatedAt != now {
		t.Fatalf("evaluation context missing: %+v", evaluation)
	}
	pr.Policy.Continuous.Sensitivity = 2
	f.bases[0].BaseWeight = 200
	if evaluation.BaseWeight != 100 || evaluation.Params.Sensitivity == 2 {
		t.Fatal("evaluation was changed by a later draft")
	}
}

func TestSupersededDecisionDoesNotCountAsWriteFailure(t *testing.T) {
	f := &continuousFake{}
	s := ContinuousState{}
	for i := 0; i < 5; i++ {
		NewEngine(f).noteWriteFailure("i", ChannelBaseValue{}, &s, "auto", ErrDecisionSuperseded, time.Now())
	}
	if s.WriteFailureStreak != 0 || s.PausedReason != "" || len(f.recommendations) != 0 {
		t.Fatalf("configuration changes must not trip the sentinel: %+v", s)
	}
}

func TestQueuedCircuitWaitsForAcknowledgementBeforeProbing(t *testing.T) {
	f := &continuousFake{bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 100}}, states: map[int64]ContinuousState{}, commandStatus: "pending"}
	addNeutralPerformanceEvidence(f)
	now := time.Now().UTC()
	pr := autoPolicy()
	state := ContinuousState{InstanceID: "i", ChannelID: 1, ModelName: "m", Phase: "circuit", ProposedWeight: 0, Evaluation: &EvaluationContext{Params: pr.Policy.Continuous}}
	e := NewEngine(f)
	_, err := e.createTrackedWeightChange(f, continuousEvent("i", f.bases[0], state, "circuit_opened", "auto", now), f.bases[0], &state, now)
	if err != nil {
		t.Fatal(err)
	}
	due := now.Add(-time.Minute)
	state.NextProbeAt = &due
	f.states[1] = state
	e.evaluateContinuous("i", pr, now.Add(time.Hour), f)
	if len(f.probes) != 0 || f.states[1].LastWrittenWeight != nil {
		t.Fatal("unconfirmed zero must not start probes or count as success")
	}
	state = f.states[1]
	f.commandStatus = "succeeded"
	e.settleCapacityWrite(f, "i", f.bases[0], &state, now.Add(time.Hour))
	if state.NextProbeAt == nil || !state.NextProbeAt.After(now.Add(time.Hour)) || *state.LastWrittenWeight != 0 {
		t.Fatalf("silence must start after confirmation: %+v", state)
	}
}
