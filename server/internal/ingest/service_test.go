package ingest

import (
	"testing"
	"time"

	"controltower/server/internal/agentgateway"
	"controltower/server/internal/storage"
)

func TestServiceStoresReportIdempotentlyAndUpdatesOffset(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	reportedAt := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)

	report := agentgateway.AgentReportRequest{
		InstanceID:        "inst-1",
		AgentID:           "agent-1",
		AgentVersion:      "0.1.0",
		ReportedAt:        reportedAt,
		Sequence:          1,
		SourceLatestLogID: 4500,
		BacklogEstimate:   4400,
		LogEvents: []agentgateway.LogEventPayload{
			{
				SourceLogID:      100,
				CreatedAt:        reportedAt,
				LogType:          "consume",
				UserID:           7,
				Username:         "alice",
				ChannelID:        18,
				ModelName:        "gpt-4o",
				PromptTokens:     10,
				CompletionTokens: 20,
				TotalTokens:      30,
				Quota:            90,
				UseTime:          2.5,
			},
		},
		DockerStatuses: []agentgateway.DockerStatusPayload{
			{
				CollectedAt:   reportedAt,
				ContainerName: "new-api",
				Status:        "Up 3 hours",
				Running:       true,
			},
		}, HealthChecks: []agentgateway.HealthCheckPayload{
			{
				CheckedAt:      reportedAt,
				Target:         "http://127.0.0.1:3000/api/status",
				Status:         "up",
				HTTPStatusCode: 200,
				LatencyMS:      12,
			},
		},
	}

	if err := service.SaveReport(report); err != nil {
		t.Fatalf("save first report: %v", err)
	}
	if err := service.SaveReport(report); err != nil {
		t.Fatalf("save duplicate report: %v", err)
	}

	if got := store.LogEventCount(); got != 1 {
		t.Fatalf("expected idempotent insert, got %d events", got)
	}
	if got := store.Offset("inst-1"); got != 100 {
		t.Fatalf("unexpected offset: %d", got)
	}
	agent, ok := store.Agent("agent-1")
	if !ok || agent.LastLogID != 100 || agent.SourceLatestLogID != 4500 || agent.BacklogEstimate != 4400 {
		t.Fatalf("agent telemetry not updated: %#v ok=%v", agent, ok)
	}
	if got := store.DockerStatusCount(); got != 2 {
		t.Fatalf("expected duplicate report to store 2 docker statuses, got %d", got)
	}
	if got := store.HealthCheckCount(); got != 2 {
		t.Fatalf("expected duplicate report to store 2 health checks, got %d", got)
	}
}

func TestServiceStoresAggregatedMetrics(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	reportedAt := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	successRate := 1.0

	err := service.SaveReport(agentgateway.AgentReportRequest{
		InstanceID:    "inst-1",
		AgentID:       "agent-1",
		AgentVersion:  "0.1.0",
		ReportedAt:    reportedAt,
		Sequence:      3,
		MetricBatchID: "agent-1:100:101",
		AggregatedMetrics: []agentgateway.AggregatedMetricPayload{
			{BucketTime: reportedAt, WindowSeconds: 60, DimensionType: "instance", DimensionKey: "inst-1", RequestCount: 2, SuccessCount: 2, SuccessRate: &successRate, TPM: 30},
		},
	})
	if err != nil {
		t.Fatalf("save report: %v", err)
	}

	metrics, err := store.Recent1mMetrics()
	if err != nil {
		t.Fatalf("recent metrics: %v", err)
	}
	if len(metrics) != 1 {
		t.Fatalf("expected one metric, got %#v", metrics)
	}
	if metrics[0].InstanceID != "inst-1" || metrics[0].RequestCount != 2 || metrics[0].SuccessRate == nil || *metrics[0].SuccessRate != 1.0 {
		t.Fatalf("unexpected metric: %#v", metrics[0])
	}
}
func TestServiceMergesDistinctMetricBatchesAndDeduplicatesRetries(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	bucket := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

	report := func(batchID string, requests, successes, failures int64, useTimeSum float64) agentgateway.AgentReportRequest {
		return agentgateway.AgentReportRequest{
			InstanceID:    "inst-1",
			AgentID:       "agent-1",
			ReportedAt:    bucket,
			MetricBatchID: batchID,
			AggregatedMetrics: []agentgateway.AggregatedMetricPayload{{
				BucketTime:    bucket,
				WindowSeconds: 60,
				DimensionType: "instance",
				DimensionKey:  "inst-1",
				RequestCount:  requests,
				SuccessCount:  successes,
				ErrorCount:    failures,
				UseTimeSum:    useTimeSum,
				StreamCount:   successes,
			}},
		}
	}

	first := report("agent-1:1:2", 2, 2, 0, 4)
	second := report("agent-1:3:5", 3, 2, 1, 9)
	for _, item := range []agentgateway.AgentReportRequest{first, second, first} {
		if err := service.SaveReport(item); err != nil {
			t.Fatalf("save metric report: %v", err)
		}
	}

	metrics, err := store.Recent1mMetrics()
	if err != nil {
		t.Fatalf("recent metrics: %v", err)
	}
	if len(metrics) != 1 {
		t.Fatalf("metrics len = %d, want 1: %#v", len(metrics), metrics)
	}
	got := metrics[0]
	if got.RequestCount != 5 || got.SuccessCount != 4 || got.ErrorCount != 1 {
		t.Fatalf("unexpected merged counts: %#v", got)
	}
	if got.AvgUseTime == nil || *got.AvgUseTime != 2.6 {
		t.Fatalf("unexpected merged average: %#v", got.AvgUseTime)
	}
	if got.StreamRate == nil || *got.StreamRate != 0.8 {
		t.Fatalf("unexpected merged stream rate: %#v", got.StreamRate)
	}
	metrics5m, err := store.Recent5mMetrics()
	if err != nil {
		t.Fatalf("recent 5m metrics: %v", err)
	}
	if len(metrics5m) != 1 || metrics5m[0].RequestCount != 5 {
		t.Fatalf("unexpected 5m metrics: %#v", metrics5m)
	}
}
func TestMemoryStoreQueriesLogEvents(t *testing.T) {
	store := NewMemoryStore()
	base := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	events := []storage.LogEvent{
		{InstanceID: "inst-1", SourceLogID: 100, CreatedAt: base, LogType: "consume", UserID: 7, ModelName: "gpt-4o"},
		{InstanceID: "inst-1", SourceLogID: 101, CreatedAt: base.Add(time.Minute), LogType: "error", UserID: 7, ModelName: "gpt-4o"},
		{InstanceID: "inst-2", SourceLogID: 200, CreatedAt: base.Add(2 * time.Minute), LogType: "error", UserID: 8, ModelName: "claude"},
	}
	for _, event := range events {
		if _, err := store.InsertLogEvent(event); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}

	got, err := store.QueryLogEvents(storage.LogQuery{InstanceID: "inst-1", UserID: 7, Limit: 1})
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	if len(got) != 1 || got[0].SourceLogID != 101 {
		t.Fatalf("unexpected query result: %#v", got)
	}

	got[0].SourceLogID = 999
	again, err := store.QueryLogEvents(storage.LogQuery{InstanceID: "inst-1", UserID: 7, Limit: 1})
	if err != nil {
		t.Fatalf("query events again: %v", err)
	}
	if again[0].SourceLogID != 101 {
		t.Fatalf("query returned store alias: %#v", again)
	}
}

func TestServiceStoresHeartbeat(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	reportedAt := time.Date(2026, 7, 2, 12, 5, 0, 0, time.UTC)

	serverLastLogID, err := service.SaveHeartbeat(agentgateway.AgentHeartbeatRequest{
		InstanceID:   "inst-1",
		AgentID:      "agent-1",
		AgentVersion: "0.1.0",
		ReportedAt:   reportedAt,
		Sequence:     2,
		LastLogID:    321,
	})
	if err != nil {
		t.Fatalf("save heartbeat: %v", err)
	}
	if serverLastLogID != 0 {
		t.Fatalf("server last log id = %d, want 0", serverLastLogID)
	}

	agent, ok := store.Agent("agent-1")
	if !ok {
		t.Fatalf("agent not stored")
	}
	if agent.InstanceID != "inst-1" || agent.LastLogID != 321 {
		t.Fatalf("unexpected agent: %#v", agent)
	}
}

func TestServiceRejectsReportWithMissingIdentity(t *testing.T) {
	service := NewService(NewMemoryStore())
	err := service.SaveReport(agentgateway.AgentReportRequest{
		AgentID: "agent-1",
	})
	if err == nil {
		t.Fatalf("expected missing instance id error")
	}
}
