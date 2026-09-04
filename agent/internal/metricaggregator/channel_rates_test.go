package metricaggregator

import (
	"controltower/agent/internal/logcollector"
	"testing"
	"time"
)

func TestChannelRatesPreservesSecondsAndConsumeOnly(t *testing.T) {
	now := time.Date(2026, 9, 4, 10, 1, 15, 0, time.UTC)
	events := []logcollector.Event{
		{ChannelID: 7, LogType: "consume", CreatedAt: now.Add(-20 * time.Second), PromptTokens: 100, CompletionTokens: 10},
		{ChannelID: 7, LogType: "consume", CreatedAt: now.Add(-20 * time.Second), PromptTokens: 200, CompletionTokens: 20},
		{ChannelID: 7, LogType: "consume", CreatedAt: now.Add(-5 * time.Second), PromptTokens: 50},
		{ChannelID: 7, LogType: "error", CreatedAt: now, PromptTokens: 999},
		{ChannelID: 7, LogType: "consume", CreatedAt: now.Add(-3 * time.Minute), PromptTokens: 999},
	}
	rates := ChannelRates(events, now)
	if len(rates) != 3 {
		t.Fatalf("unexpected rates: %#v", rates)
	}
	var requests, tokens int64
	for _, rate := range rates {
		if rate.WindowSeconds != 1 || rate.DimensionType != "channel_rate_second" {
			t.Fatal(rate)
		}
		requests += rate.RequestCount
		tokens += rate.TPM
		if rate.RequestCount == 2 && !rate.BucketTime.Equal(now.Add(-20*time.Second)) {
			t.Fatal("lost second resolution")
		}
	}
	if requests != 3 || tokens != 380 {
		t.Fatalf("totals %d %d", requests, tokens)
	}
	if quiet := ChannelRates(nil, now); len(quiet) != 1 || quiet[0].DimensionKey != "0" {
		t.Fatal("missing idle coverage marker")
	}
}
