package tuning

import (
	"math"
	"testing"
	"time"
)

func TestCurrentOTPSReplacesMissingTTFT(t *testing.T) {
	p := DefaultPolicy().Continuous
	p.MinSamples = 5
	p.OTPSExponent = 1
	p.OTPSMinFactor = .1
	p.OTPSMaxFactor = 3
	p.CombinedMaxFactor = 10
	m := ChannelMetric{RequestCount: 100, OTPS: 120, OTPSSamples: 20, OTPSSampleTokens: 1000, OTPSStatsVersion: 1}
	b := continuousBaseline{otps: 100, otpsReady: true}
	s := ContinuousState{KSpeed: .713, KError: 1, OTPSReady: true}
	applyPerformanceFactors(&s, m, b, p)
	if s.KSpeed != 1.2 || s.KOTPS != 1.2 || math.Abs(combinedFactor(s, p)-1.44) > 1e-9 {
		t.Fatalf("not current OTPS squared: %+v", s)
	}
	m.OTPS = 80
	applyPerformanceFactors(&s, m, b, p)
	if s.KSpeed != .8 || math.Abs(combinedFactor(s, p)-.64) > 1e-9 {
		t.Fatalf("reused old score: %+v", s)
	}
	m.SpeedSamples = 20
	m.SpeedTTFTP50 = 1
	m.SpeedTTFTP90 = 1
	m.SpeedTTFTP95 = 1
	b.speedReady = true
	b.ttft50 = 1
	b.ttft90 = 1
	b.ttft95 = 1
	s.MetricReady = true
	applyPerformanceFactors(&s, m, b, p)
	if s.KSpeed != 1 || s.KOTPS != .8 {
		t.Fatalf("TTFT did not resume: %+v", s)
	}
	b.speedReady = false
	applyPerformanceFactors(&s, m, b, p)
	if s.KSpeed != s.KOTPS {
		t.Fatal("missing peer baseline did not use output")
	}
}

func TestSparseWindowDoesNotWriteButCircuitStillRuns(t *testing.T) {
	now := time.Now().UTC()
	lastWeight := int64(68)
	lastAt := now.Add(-time.Minute)
	f := &continuousFake{bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", BaseWeight: 50, CurrentWeight: 68}, {ChannelID: 2, ModelName: "m", BaseWeight: 50, CurrentWeight: 50}}, states: map[int64]ContinuousState{1: {ChannelID: 1, ModelName: "m", Phase: "normal", KSpeed: 1.366, KError: 1, SpeedStatsVersion: 1, LastWrittenWeight: &lastWeight, LastWriteAt: &lastAt}}, metrics: []ChannelMetric{{ChannelID: 1, RequestCount: 1}, {ChannelID: 2, RequestCount: 100, OTPS: 100, OTPSSamples: 20, OTPSSampleTokens: 1000, OTPSStatsVersion: 1}}}
	p := autoPolicy()
	p.Policy.Continuous.MinSamples = 5
	NewEngine(f).evaluateContinuous("i", p, now, f)
	if f.states[1].ProposedWeight != 68 || f.states[1].KSpeed != 1 {
		t.Fatalf("sparse state %+v", f.states[1])
	}
	for _, w := range f.writes {
		if w.ChannelID == 1 {
			t.Fatal("sparse channel was rewritten")
		}
	}
	f.metrics[0].RequestCount = 100
	s := f.states[1]
	s.SmoothedErrorRate = .9
	s.KError = .2
	f.states[1] = s
	NewEngine(f).evaluateContinuous("i", p, now.Add(time.Second), f)
	if f.states[1].Phase != "circuit" {
		t.Fatalf("missing speed evidence blocked circuit: %+v", f.states[1])
	}
}

func TestOTPSFallbackChangesActualWeightsAndKeepsCache(t *testing.T) {
	now := time.Now().UTC()
	f := &continuousFake{bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", BaseWeight: 100, CurrentWeight: 100}, {ChannelID: 2, ModelName: "m", BaseWeight: 100, CurrentWeight: 100}}, states: map[int64]ContinuousState{1: {ChannelID: 1, ModelName: "m", KSpeed: .713, SpeedStatsVersion: 1}}, metrics: []ChannelMetric{
		{ChannelID: 1, RequestCount: 100, OTPS: 120, OTPSSamples: 20, OTPSSampleTokens: 1000, OTPSStatsVersion: 1},
		{ChannelID: 2, RequestCount: 100, OTPS: 80, OTPSSamples: 20, OTPSSampleTokens: 1000, OTPSStatsVersion: 1},
	}}
	p := autoPolicy()
	p.Policy.Continuous.MinSamples = 5
	p.Policy.Continuous.OTPSExponent = 1
	p.Policy.Continuous.OTPSMinFactor = .1
	p.Policy.Continuous.OTPSMaxFactor = 3
	p.Policy.Continuous.CombinedMaxFactor = 10
	p.Policy.Continuous.MaxIncreasePercent = 100
	NewEngine(f).evaluateContinuous("i", p, now, f)
	if f.states[1].ProposedWeight != 144 || f.states[2].ProposedWeight != 64 || len(f.writes) != 2 {
		t.Fatalf("fallback factors did not reach actual dispatch: %+v writes=%+v", f.states, f.writes)
	}
	// Cache remains an independent multiplier under the existing policy.
	s := f.states[1]
	s.KCache = 1.05
	if math.Abs(combinedFactor(s, p.Policy.Continuous)-1.512) > 1e-9 {
		t.Fatal("cache no longer participates")
	}
	// Invalid current output evidence must not recycle either old speed factor.
	f.metrics[0].OTPSSamples = 0
	f.metrics[1].OTPSSamples = 0
	f.writes = nil
	NewEngine(f).evaluateContinuous("i", p, now.Add(time.Minute), f)
	if len(f.writes) != 0 || f.states[1].ProposedWeight != 144 || f.states[1].KSpeed != 1 {
		t.Fatalf("missing output changed dispatch or reused historical TTFT: %+v", f.states[1])
	}
}
