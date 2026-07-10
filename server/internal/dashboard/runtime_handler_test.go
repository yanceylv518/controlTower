package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controltower/server/internal/storage"
)

func TestHandleRuntimeQueriesReturnItemsAndParseFilters(t *testing.T) {
	base := time.Now().UTC().Add(-30 * time.Second).Truncate(time.Second)
	running := true
	store := &capturingRuntimeStore{
		agents:         []storage.Agent{{ID: "agent-1", InstanceID: "inst-1", Version: "0.1.0", LastSeenAt: base, LastSequence: 99, LastLogID: 123, SourceLatestLogID: 456, BacklogEstimate: 333, Status: "ok", ReportDelayMS: 10}},
		metrics:        []storage.ServerMetric{{InstanceID: "inst-1", CollectedAt: base, CPUPercent: 12.5}},
		healthChecks:   []storage.HealthCheck{{InstanceID: "inst-1", CheckedAt: base, Target: "http://127.0.0.1/status", Status: "up", HTTPStatusCode: 200, LatencyMS: 8}},
		dockerStatuses: []storage.DockerStatus{{InstanceID: "inst-1", CollectedAt: base, ContainerName: "new-api", Status: "Up 1 hour", Running: true}},
	}
	handler := NewHandler(staticOverviewSource{}).WithRuntimeStore(store)

	agentReq := httptest.NewRequest(http.MethodGet, "/api/dashboard/agents?instance_id=inst-1&status=ok&limit=10&offset=2", nil)
	agentRR := httptest.NewRecorder()
	handler.HandleAgents(agentRR, agentReq)
	if agentRR.Code != http.StatusOK {
		t.Fatalf("agents status = %d body=%s", agentRR.Code, agentRR.Body.String())
	}
	var agentResp AgentListResponse
	if err := json.Unmarshal(agentRR.Body.Bytes(), &agentResp); err != nil {
		t.Fatalf("decode agents: %v", err)
	}
	if len(agentResp.Items) != 1 || agentResp.Items[0].ID != "agent-1" || !agentResp.Items[0].Online || agentResp.Items[0].SourceLatestLogID != 456 || agentResp.Items[0].BacklogEstimate != 333 {
		t.Fatalf("unexpected agents response: %#v", agentResp)
	}
	if store.agentQuery.InstanceID != "inst-1" || store.agentQuery.Status != "ok" || store.agentQuery.Limit != 10 || store.agentQuery.Offset != 2 {
		t.Fatalf("agent query not parsed: %#v", store.agentQuery)
	}

	metricReq := httptest.NewRequest(http.MethodGet, "/api/dashboard/server-metrics?instance_id=inst-1&start_time=2026-07-07T11:00:00Z&end_time=2026-07-07T13:00:00Z&limit=25&offset=5", nil)
	metricRR := httptest.NewRecorder()
	handler.HandleServerMetrics(metricRR, metricReq)
	if metricRR.Code != http.StatusOK {
		t.Fatalf("metrics status = %d body=%s", metricRR.Code, metricRR.Body.String())
	}
	var metricResp ServerMetricListResponse
	if err := json.Unmarshal(metricRR.Body.Bytes(), &metricResp); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	if len(metricResp.Items) != 1 || metricResp.Items[0].CPUPercent != 12.5 {
		t.Fatalf("unexpected metrics response: %#v", metricResp)
	}
	if store.metricQuery.InstanceID != "inst-1" || store.metricQuery.Limit != 25 || store.metricQuery.Offset != 5 || store.metricQuery.StartTime.IsZero() || store.metricQuery.EndTime.IsZero() {
		t.Fatalf("metric query not parsed: %#v", store.metricQuery)
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/api/dashboard/health-checks?instance_id=inst-1&target=http://127.0.0.1/status&status=up&limit=10", nil)
	healthRR := httptest.NewRecorder()
	handler.HandleHealthChecks(healthRR, healthReq)
	if healthRR.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s", healthRR.Code, healthRR.Body.String())
	}
	if store.healthQuery.Target != "http://127.0.0.1/status" || store.healthQuery.Status != "up" {
		t.Fatalf("health query not parsed: %#v", store.healthQuery)
	}

	dockerReq := httptest.NewRequest(http.MethodGet, "/api/dashboard/docker-statuses?instance_id=inst-1&container_name=new-api&running=true", nil)
	dockerRR := httptest.NewRecorder()
	handler.HandleDockerStatuses(dockerRR, dockerReq)
	if dockerRR.Code != http.StatusOK {
		t.Fatalf("docker status = %d body=%s", dockerRR.Code, dockerRR.Body.String())
	}
	if store.dockerQuery.ContainerName != "new-api" || store.dockerQuery.Running == nil || *store.dockerQuery.Running != running {
		t.Fatalf("docker query not parsed: %#v", store.dockerQuery)
	}
}

func TestHandleRuntimeQueriesRequireStoreAndGET(t *testing.T) {
	handler := NewHandler(staticOverviewSource{})
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/agents", nil)
	rr := httptest.NewRecorder()
	handler.HandleAgents(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 without store, got %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/dashboard/docker-statuses", nil)
	rr = httptest.NewRecorder()
	handler.WithRuntimeStore(&capturingRuntimeStore{}).HandleDockerStatuses(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST, got %d", rr.Code)
	}
}

type capturingRuntimeStore struct {
	agentQuery     storage.AgentQuery
	metricQuery    storage.ServerMetricQuery
	healthQuery    storage.HealthCheckQuery
	dockerQuery    storage.DockerStatusQuery
	agents         []storage.Agent
	metrics        []storage.ServerMetric
	healthChecks   []storage.HealthCheck
	dockerStatuses []storage.DockerStatus
}

func (s *capturingRuntimeStore) QueryAgents(query storage.AgentQuery) ([]storage.Agent, error) {
	s.agentQuery = query
	return s.agents, nil
}

func (s *capturingRuntimeStore) QueryServerMetrics(query storage.ServerMetricQuery) ([]storage.ServerMetric, error) {
	s.metricQuery = query
	return s.metrics, nil
}

func (s *capturingRuntimeStore) QueryHealthChecks(query storage.HealthCheckQuery) ([]storage.HealthCheck, error) {
	s.healthQuery = query
	return s.healthChecks, nil
}

func (s *capturingRuntimeStore) QueryDockerStatuses(query storage.DockerStatusQuery) ([]storage.DockerStatus, error) {
	s.dockerQuery = query
	return s.dockerStatuses, nil
}
