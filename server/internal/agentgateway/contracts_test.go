package agentgateway

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAgentReportRequestJSONContract(t *testing.T) {
	reportedAt := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	cacheTokens := int64(128)

	req := AgentReportRequest{
		InstanceID:   "inst-hdu",
		AgentID:      "agent-hdu-01",
		AgentVersion: "0.1.0",
		ReportedAt:   reportedAt,
		Sequence:     42,
		LastLogID:    1001,
		LogSamples:   []LogSamplePayload{{SampleKind: "error", SourceLogID: 9001, CreatedAt: reportedAt, LogType: "error"}},
		LogEvents: []LogEventPayload{
			{
				SourceLogID:       1001,
				CreatedAt:         reportedAt.Add(-time.Minute),
				LogType:           "consume",
				UserID:            7,
				Username:          "alice",
				ChannelID:         18,
				ModelName:         "gpt-4o",
				TokenID:           9,
				TokenName:         "prod-token",
				PromptTokens:      30,
				CompletionTokens:  70,
				TotalTokens:       100,
				Quota:             500,
				UseTime:           3.2,
				IsStream:          true,
				Group:             "default",
				RequestID:         "req-1",
				UpstreamRequestID: "up-1",
				CacheTokens:       &cacheTokens,
				CacheFieldPresent: true,
			},
		},
		ServerMetrics: []ServerMetricPayload{
			{
				CollectedAt:             reportedAt,
				CPUPercent:              20.5,
				MemoryUsedPercent:       66.1,
				DiskUsedPercent:         71.2,
				NetworkRxBytesPerSecond: 1000,
				NetworkTxBytesPerSecond: 2000,
				Load1m:                  0.7,
			},
		},
		HealthChecks: []HealthCheckPayload{
			{
				CheckedAt:      reportedAt,
				Target:         "new-api",
				Status:         "healthy",
				HTTPStatusCode: 200,
				LatencyMS:      15,
			},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal report map: %v", err)
	}

	for _, key := range []string{"instance_id", "agent_id", "agent_version", "reported_at", "sequence", "last_log_id", "log_events", "log_samples", "aggregated_metrics", "server_metrics", "health_checks"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("missing json key %q in %s", key, string(data))
		}
	}

	var roundTrip AgentReportRequest
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("round trip report: %v", err)
	}
	if roundTrip.InstanceID != req.InstanceID || roundTrip.Sequence != req.Sequence {
		t.Fatalf("round trip mismatch: %#v", roundTrip)
	}
	if roundTrip.LogEvents[0].CacheTokens == nil || *roundTrip.LogEvents[0].CacheTokens != cacheTokens {
		t.Fatalf("cache tokens not preserved: %#v", roundTrip.LogEvents[0].CacheTokens)
	}
}

func TestHeartbeatRequestJSONContract(t *testing.T) {
	reportedAt := time.Date(2026, 7, 2, 12, 5, 0, 0, time.UTC)
	req := AgentHeartbeatRequest{
		InstanceID:   "inst-hdu",
		AgentID:      "agent-hdu-01",
		AgentVersion: "0.1.0",
		ReportedAt:   reportedAt,
		Sequence:     7,
		LastLogID:    12345,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal heartbeat: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal heartbeat map: %v", err)
	}

	for _, key := range []string{"instance_id", "agent_id", "agent_version", "reported_at", "sequence", "last_log_id"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("missing json key %q in %s", key, string(data))
		}
	}
}
