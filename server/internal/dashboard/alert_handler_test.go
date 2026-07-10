package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controltower/server/internal/aggregator"
	"controltower/server/internal/storage"
)

func TestBuildCurrentAlertsDetectsMetricAndRuntimeIssues(t *testing.T) {
	errorRate := 0.5
	p95 := 11.0
	base := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)

	alerts := BuildCurrentAlerts(
		[]aggregator.Metric{{InstanceID: "inst-1", BucketTime: base, DimensionType: "instance", DimensionKey: "inst-1", RequestCount: 10, ErrorCount: 5, ErrorRate: &errorRate, P95UseTime: &p95}},
		[]storage.ServerMetric{{InstanceID: "inst-1", CollectedAt: base, CPUPercent: 91, MemoryUsedPercent: 70, DiskUsedPercent: 96}},
		[]storage.HealthCheck{{InstanceID: "inst-1", CheckedAt: base, Target: "status", Status: "down", HTTPStatusCode: 500, LatencyMS: 1200}},
		[]storage.DockerStatus{{InstanceID: "inst-1", CollectedAt: base, ContainerName: "new-api", Status: "Exited", Running: false}},
	)

	if len(alerts) != 6 {
		t.Fatalf("alerts len = %d, want 6: %#v", len(alerts), alerts)
	}
	if alerts[0].Severity != "critical" || alerts[0].Status != "firing" {
		t.Fatalf("critical firing alert should sort first: %#v", alerts[0])
	}
}

func TestAppendAgentBacklogAlertsAppliesThresholdsAndStaleness(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	alerts := appendAgentBacklogAlerts(nil, []storage.Agent{
		{ID: "warning-agent", InstanceID: "inst-1", LastSeenAt: now.Add(-30 * time.Second), LastLogID: 1000, SourceLatestLogID: 4000, BacklogEstimate: 3000},
		{ID: "critical-agent", InstanceID: "inst-2", LastSeenAt: now, LastLogID: 2000, SourceLatestLogID: 12000, BacklogEstimate: 10000},
		{ID: "stale-agent", InstanceID: "inst-3", LastSeenAt: now.Add(-3 * time.Minute), BacklogEstimate: 20000},
	}, now)
	if len(alerts) != 2 {
		t.Fatalf("alerts len = %d, want 2: %#v", len(alerts), alerts)
	}
	if alerts[0].Severity != "critical" || alerts[0].RuleKey != "agent_backlog" {
		t.Fatalf("unexpected first alert: %#v", alerts[0])
	}
	if alerts[1].Severity != "warning" {
		t.Fatalf("unexpected second alert: %#v", alerts[1])
	}
}

func TestParseAlertQuerySupportsFilters(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/alerts?status=resolved&severity=warning&active_only=true&limit=10", nil)
	query := parseAlertQuery(req)
	if query.Status != "resolved" || query.Severity != "warning" || !query.ActiveOnly || query.Limit != 10 {
		t.Fatalf("unexpected query: %#v", query)
	}
}

func TestHandleAlertsReturnsJSON(t *testing.T) {
	errorRate := 0.25
	handler := NewHandler(staticOverviewSource{metrics: []aggregator.Metric{{InstanceID: "inst-1", BucketTime: time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC), DimensionType: "instance", DimensionKey: "inst-1", RequestCount: 8, ErrorCount: 2, ErrorRate: &errorRate}}})

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/alerts", nil)
	rr := httptest.NewRecorder()
	handler.HandleAlerts(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var response AlertListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Items) != 1 || response.Items[0].RuleKey != "high_error_rate" {
		t.Fatalf("unexpected alerts: %#v", response.Items)
	}
}

func TestHandleAlertsRejectsNonGET(t *testing.T) {
	handler := NewHandler(staticOverviewSource{})
	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/alerts", nil)
	rr := httptest.NewRecorder()
	handler.HandleAlerts(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}
