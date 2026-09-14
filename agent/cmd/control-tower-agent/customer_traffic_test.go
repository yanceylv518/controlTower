package main

import (
	"context"
	"testing"
	"time"

	"controltower/agent/internal/config"
	"controltower/agent/internal/logcollector"
)

func TestReportCustomerTrafficCannotDisplaceRateMetrics(t *testing.T) {
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	for _, count := range []int{2000, 3000} {
		events := make([]logcollector.Event, count)
		for i := range events {
			events[i] = logcollector.Event{CreatedAt: now, SourceLogID: int64(i + 1), UserID: int64(i + 1), ChannelID: 3, ModelName: "a", LogType: "consume", TotalTokens: 30, PromptTokens: 20, CompletionTokens: 10}
		}
		report := buildReport(context.Background(), config.Config{InstanceID: "site", AgentID: "test", LogCollectEnabled: true}, now, 1, int64(count), logcollector.BacklogStats{SnapshotKnown: true}, events, nil, nil, nil, nil)
		traffic, users, rates := 0, 0, 0
		for _, m := range report.AggregatedMetrics {
			switch m.DimensionType {
			case "instance_user_channel":
				traffic++
				if m.TPM != 30 {
					t.Fatal("invalid channel TPM")
				}
			case "instance_user":
				users++
				if m.TPM != 30 || m.RequestCount != 1 {
					t.Fatal("existing user metric changed")
				}
			case "channel_rate_second":
				rates++
				if m.DimensionKey == "3" && (m.TPM != int64(count*30) || m.RequestCount != int64(count)) {
					t.Fatal("rate counter changed")
				}
			}
		}
		if len(report.AggregatedMetrics) > 10000 || users != count || rates != 2 {
			t.Fatalf("existing report affected: rows=%d users=%d rates=%d", len(report.AggregatedMetrics), users, rates)
		}
		if (count == 2000 && traffic != count) || (count == 3000 && traffic != 0) {
			t.Fatalf("incorrect budget behavior: count=%d traffic=%d", count, traffic)
		}
	}
}
