package tuning

import (
	"math"
	"testing"
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
