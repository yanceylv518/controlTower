package tuning

import (
	"testing"
	"time"
)

func TestDirectSpeedDoesNotChangeProbeRecoveryThreshold(t *testing.T) {
	for _, tc := range []struct {
		direct, duration float64
		phase            string
	}{{0, 400, "circuit"}, {1, 100, "soft_start"}} {
		direct := tc.direct
		f := &continuousFake{
			bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", BaseWeight: 100}, {ChannelID: 2, ModelName: "m", BaseWeight: 100}, {ChannelID: 3, ModelName: "m", BaseWeight: 100}},
			metrics: []ChannelMetric{
				{ChannelID: 1, RequestCount: 100, TTFTP50: 1, TTFTP90: 2, TTFTP95: 4, SpeedSamples: 20, SpeedTTFTP50: direct, SpeedTTFTP90: direct, SpeedTTFTP95: direct},
				{ChannelID: 2, RequestCount: 100, TTFTP50: 1, TTFTP90: 2, TTFTP95: 4, SpeedSamples: 20, SpeedTTFTP50: direct, SpeedTTFTP90: direct, SpeedTTFTP95: direct},
			},
			states: map[int64]ContinuousState{3: {ChannelID: 3, ModelName: "m", Phase: "probing", KError: .1, ProbeAttempts: 10, ProbeSuccesses: 10, ProbeDurationSum: tc.duration}},
		}
		p := autoPolicy()
		p.Policy.Continuous.RecoveryThreshold = .5
		NewEngine(f).evaluateContinuous("i", p, time.Now().UTC(), f)
		if f.states[3].Phase != tc.phase {
			t.Fatalf("direct TTFT %v changed slow-probe recovery: %+v", direct, f.states[3])
		}
	}
}

func TestExcludedChannelsDoNotRetainSpeedFactor(t *testing.T) {
	for _, base := range []ChannelBaseValue{{ChannelID: 1, ModelName: "m", BaseWeight: 0}, {ChannelID: 1, ModelName: "m", BaseWeight: 100, Models: []string{"m", "n"}}} {
		f := &continuousFake{bases: []ChannelBaseValue{base}, states: map[int64]ContinuousState{1: {ChannelID: 1, ModelName: "m", KSpeed: 1.5, KError: 1, SpeedStatsVersion: 1}}}
		NewEngine(f).evaluateContinuous("i", autoPolicy(), time.Now().UTC(), f)
		if f.states[1].KSpeed != 1 {
			t.Fatalf("excluded channel kept active speed factor: %+v", f.states[1])
		}
	}
}

func TestSpeedBaselineDoesNotUsePublicTTFTOrChangeOtherBaselines(t *testing.T) {
	rows := []ChannelBaseValue{{ChannelID: 1}, {ChannelID: 2}, {ChannelID: 3}}
	metrics := map[int64]ChannelMetric{}
	for i := int64(1); i <= 3; i++ {
		metrics[i] = ChannelMetric{ChannelID: i, RequestCount: 100, TTFTP50: 30, TTFTP90: 60, TTFTP95: 90, SpeedSamples: 20, SpeedTTFTP50: float64(i), SpeedTTFTP90: float64(i), SpeedTTFTP95: float64(i), CachePromptTokens: cacheEvidenceTokens, CacheHitRate: .5, OTPSSampleTokens: otpsEvidenceTokens, OTPSSamples: 20, OTPSStatsVersion: 1, OTPS: 10}
	}
	m := metrics[3]
	m.SpeedSamples = 1
	m.SpeedRetries = 99
	metrics[3] = m
	b, healthy := buildContinuousBaseline(rows, metrics, 20)
	if !healthy || !b.speedReady || b.ttft50 != 1.5 || b.cache != .5 || b.otps != 10 {
		t.Fatalf("baseline %+v", b)
	}
	m = metrics[2]
	m.SpeedSamples = 0
	metrics[2] = m
	b, healthy = buildContinuousBaseline(rows, metrics, 20)
	if !healthy || b.speedReady || !b.cacheReady || !b.otpsReady {
		t.Fatalf("speed shortage disabled other metrics: %+v", b)
	}
}

func TestInsufficientDirectSamplesNeverRetainsHistoricalSpeedScore(t *testing.T) {
	for _, version := range []int{0, 1} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			f := &continuousFake{bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", BaseWeight: 100}, {ChannelID: 2, ModelName: "m", BaseWeight: 100}},
				metrics: []ChannelMetric{{ChannelID: 1, RequestCount: 100, TTFTP50: 50, TTFTP90: 60, TTFTP95: 90, SpeedRetries: 100, CachePromptTokens: cacheEvidenceTokens, CacheHitRate: .8}, {ChannelID: 2, RequestCount: 100, TTFTP50: 1, TTFTP90: 2, TTFTP95: 3, CachePromptTokens: cacheEvidenceTokens, CacheHitRate: .2}},
				states:  map[int64]ContinuousState{1: {ChannelID: 1, ModelName: "m", KSpeed: 1.2, KError: 1, SpeedStatsVersion: version}},
			}
			p := DefaultPolicy()
			p.DispatchModes = map[string]string{"m": "observe"}
			NewEngine(f).evaluateContinuous("i", PolicyRecord{Policy: p}, time.Now().UTC(), f)
			s := f.states[1]
			want := 1.0
			if s.KSpeed != want || s.MetricReady || s.BaselineReady || s.SpeedRetries != 100 || s.SpeedStatsVersion != 1 {
				t.Fatalf("state %+v", s)
			}
			if s.KCache <= 1 {
				t.Fatal("speed shortage disabled cache scoring")
			}
		})
	}
}
