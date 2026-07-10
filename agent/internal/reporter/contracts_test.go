package reporter

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAgentContractMatchesServerJSONShape(t *testing.T) {
	reportedAt := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	req := AgentReportRequest{
		InstanceID:   "inst-hdu",
		AgentID:      "agent-hdu-01",
		AgentVersion: "0.1.0",
		ReportedAt:   reportedAt,
		Sequence:     1,
		LastLogID:    2001,
		LogSamples:   []LogSamplePayload{{SampleKind: "error", SourceLogID: 9001, CreatedAt: reportedAt, LogType: "error"}},
		LogEvents: []LogEventPayload{
			{
				SourceLogID:       2001,
				CreatedAt:         reportedAt,
				LogType:           "error",
				UserID:            8,
				Username:          "bob",
				ChannelID:         19,
				ModelName:         "claude-sonnet-4",
				PromptTokens:      11,
				CompletionTokens:  0,
				TotalTokens:       11,
				Quota:             0,
				UseTime:           9.5,
				ErrorSummary:      "upstream timeout",
				CacheFieldPresent: false,
			},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal agent report: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode json: %v", err)
	}

	if decoded["instance_id"] != "inst-hdu" {
		t.Fatalf("unexpected instance_id: %#v", decoded["instance_id"])
	}
	if _, ok := decoded["aggregated_metrics"]; !ok {
		t.Fatalf("missing aggregated_metrics in %s", string(data))
	}
	if _, ok := decoded["log_events"]; !ok {
		t.Fatalf("missing log_events in %s", string(data))
	}
}
