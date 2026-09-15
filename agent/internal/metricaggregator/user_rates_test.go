package metricaggregator

import (
	"controltower/agent/internal/logcollector"
	"testing"
	"time"
)

func TestUserRatesIsolationAndFiltering(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	events := []logcollector.Event{{UserID: 7, ChannelID: 1, LogType: "consume", CreatedAt: now.Add(-time.Second), PromptTokens: 100, CompletionTokens: 20}, {UserID: 7, ChannelID: 2, LogType: "consume", CreatedAt: now.Add(-time.Second), PromptTokens: 200}, {UserID: 8, LogType: "consume", CreatedAt: now.Add(-time.Second), PromptTokens: 10}, {UserID: 7, LogType: "error", CreatedAt: now, PromptTokens: 999}, {UserID: 7, LogType: "consume", CreatedAt: now.Add(-11 * time.Minute), PromptTokens: 999}, {UserID: 7, LogType: "consume", CreatedAt: now.Add(time.Minute), PromptTokens: 999}}
	got := UserRates(events, now)
	if len(got) != 3 || got[0].DimensionKey != "0" {
		t.Fatal(got)
	}
	for _, m := range got[1:] {
		if m.DimensionKey == "7" && (m.TPM != 320 || m.RequestCount != 2) {
			t.Fatal(m)
		}
		if m.DimensionKey == "8" && m.TPM != 10 {
			t.Fatal(m)
		}
	}
	if len(UserRates(nil, now)) != 1 {
		t.Fatal("quiet poll needs coverage")
	}
}
