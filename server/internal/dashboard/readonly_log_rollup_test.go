package dashboard

import (
	"testing"
	"time"
)

func TestAggregateReadonlyLogsGroupsDimensionsByUTC(t *testing.T) {
	items := []readonlySourceLog{
		{ID: 1, CreatedAt: time.Date(2026, 8, 5, 12, 1, 0, 0, time.UTC).Unix(), LogType: 2, UserID: 9, Username: "alice", ChannelID: 7, ModelName: "glm-5.2", TokenName: "prod", GroupName: "vip", PromptTokens: 10, CompletionTokens: 3, Quota: 4},
		{ID: 2, CreatedAt: time.Date(2026, 8, 5, 12, 59, 0, 0, time.UTC).Unix(), LogType: 2, UserID: 9, Username: "alice", ChannelID: 7, ModelName: "glm-5.2", TokenName: "prod", GroupName: "vip", PromptTokens: 20, CompletionTokens: 6, Quota: 8},
		{ID: 3, CreatedAt: time.Date(2026, 8, 5, 13, 0, 0, 0, time.UTC).Unix(), LogType: 5, UserID: 9, Username: "alice", ChannelID: 7, ModelName: "glm-5.2"},
	}
	values := aggregateReadonlyLogs("cn", items)
	if len(values) != 2 {
		t.Fatalf("rollup groups = %d, want 2", len(values))
	}
	for _, value := range values {
		if value.LogType == 2 {
			if value.RequestCount != 2 || value.PromptTokens != 30 || value.CompletionTokens != 9 || value.QuotaSum != 12 {
				t.Fatalf("consume rollup = %+v", value)
			}
			if value.Username != "alice" {
				t.Fatalf("username = %q", value.Username)
			}
		}
	}
}

func TestCompleteHourWindow(t *testing.T) {
	start := time.Date(2026, 8, 5, 0, 8, 0, 0, time.UTC)
	end := time.Date(2026, 8, 5, 12, 8, 0, 0, time.UTC)
	from, to, ok := completeHourWindow(start, end)
	if !ok || !from.Equal(time.Date(2026, 8, 5, 1, 0, 0, 0, time.UTC)) || !to.Equal(time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("window = %s..%s ok=%v", from, to, ok)
	}
}
