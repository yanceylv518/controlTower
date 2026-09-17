package tuning

import (
	"testing"
	"time"
)

func TestOutputSpeedWithoutTTFTParticipatesIndependently(t *testing.T) {
	f := &continuousFake{bases: []ChannelBaseValue{{ChannelID: 1, ModelName: "m", BaseWeight: 100}, {ChannelID: 2, ModelName: "m", BaseWeight: 100}}, metrics: []ChannelMetric{
		{ChannelID: 1, RequestCount: 100, OTPS: 40, OTPSSampleTokens: 1000, OTPSSamples: 20, OTPSStatsVersion: 1},
		{ChannelID: 2, RequestCount: 100, OTPS: 20, OTPSSampleTokens: 1000, OTPSSamples: 20, OTPSStatsVersion: 1},
	}, states: map[int64]ContinuousState{}}
	p := autoPolicy()
	p.Policy.Continuous.MinSamples = 5
	NewEngine(f).evaluateContinuous("i", p, time.Now().UTC(), f)
	if !f.states[1].OTPSReady || f.states[1].KOTPS <= 1 || f.states[2].KOTPS >= 1 || f.states[1].MetricReady || f.states[1].BaselineReady || f.states[1].KSpeed != f.states[1].KOTPS {
		t.Fatalf("states %+v", f.states)
	}
	// Hundreds of total requests cannot substitute for five direct output samples.
	f.metrics[0].OTPSSamples = 4
	f.metrics[0].OTPSRetries = 96
	NewEngine(f).evaluateContinuous("i", p, time.Now().UTC(), f)
	if f.states[1].OTPSReady || f.states[1].KOTPS != 1 || f.states[2].OTPSReady || f.states[1].OTPSRetries != 96 {
		t.Fatalf("ineligible baseline %+v", f.states)
	}
	// Legacy evidence may look sufficient but must never become new-formula input.
	f.metrics[0].OTPSSamples = 20
	f.metrics[0].OTPSStatsVersion = 0
	NewEngine(f).evaluateContinuous("i", p, time.Now().UTC(), f)
	if f.states[1].OTPSReady || f.states[1].KOTPS != 1 {
		t.Fatal("legacy score used")
	}
}
