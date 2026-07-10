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

type staticOverviewSource struct {
	metrics []aggregator.Metric
}

func (s staticOverviewSource) Recent1mMetrics() ([]aggregator.Metric, error) {
	return s.metrics, nil
}

func TestHandleOverviewReturnsJSON(t *testing.T) {
	successRate := 1.0
	handler := NewHandler(staticOverviewSource{
		metrics: []aggregator.Metric{
			{
				InstanceID:    "inst-1",
				BucketTime:    time.Date(2026, 7, 2, 12, 45, 0, 0, time.UTC),
				DimensionType: "instance",
				DimensionKey:  "inst-1",
				RequestCount:  10,
				SuccessCount:  10,
				SuccessRate:   &successRate,
				TPM:           100,
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/overview", nil)
	rr := httptest.NewRecorder()
	handler.HandleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected content type: %s", rr.Header().Get("Content-Type"))
	}

	var overview Overview
	if err := json.Unmarshal(rr.Body.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if overview.InstanceCount != 1 || overview.Recent1m.RequestCount != 10 {
		t.Fatalf("unexpected overview: %#v", overview)
	}
}

func TestHandleOverviewRejectsNonGET(t *testing.T) {
	handler := NewHandler(staticOverviewSource{})
	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/overview", nil)
	rr := httptest.NewRecorder()
	handler.HandleOverview(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}
func TestHandleOverviewIncludesRuntimeSummary(t *testing.T) {
	base := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	handler := NewHandler(staticOverviewSource{}).WithRuntimeStore(&capturingRuntimeStore{
		metrics:        []storage.ServerMetric{{InstanceID: "inst-1", CollectedAt: base, CPUPercent: 12}},
		healthChecks:   []storage.HealthCheck{{InstanceID: "inst-1", CheckedAt: base, Target: "status", Status: "up"}},
		dockerStatuses: []storage.DockerStatus{{InstanceID: "inst-1", CollectedAt: base, ContainerName: "api", Running: true}},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/overview", nil)
	rr := httptest.NewRecorder()
	handler.HandleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var overview Overview
	if err := json.Unmarshal(rr.Body.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if len(overview.Runtime.LatestServerMetrics) != 1 || overview.Runtime.Health.UpCount != 1 || overview.Runtime.Docker.RunningCount != 1 {
		t.Fatalf("runtime summary missing: %#v", overview.Runtime)
	}
}
