package dashboard

import (
	"bytes"
	"controltower/server/internal/aggregator"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInstanceCreateRotateAndDisable(t *testing.T) {
	s := ingest.NewMemoryStore()
	h := InstanceHandler{Store: s, Runtime: s, Pepper: "pep"}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"instance_id":"inst-a","name":"A"}`))
	h.Create(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	var out map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out["token"] == "" {
		t.Fatal("missing token")
	}
	if id, ok, _ := s.InstanceIDByTokenHash(tokenHash("pep", out["token"]), time.Now()); !ok || id != "inst-a" {
		t.Fatal("token lookup failed")
	}
	w = httptest.NewRecorder()
	h.Create(w, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"instance_id":"BAD","name":"x"}`)))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	h.List(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(w.Body.String(), "token") {
		t.Fatal("token leaked")
	}
}

func TestInstanceListUsesSnakeCaseDTO(t *testing.T) {
	s := ingest.NewMemoryStore()
	h := InstanceHandler{Store: s, Runtime: s, Pepper: "pep"}
	w := httptest.NewRecorder()
	h.Create(w, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"instance_id":"inst-dto","name":"D"}`)))
	w = httptest.NewRecorder()
	h.List(w, httptest.NewRequest(http.MethodGet, "/", nil))
	body := w.Body.String()
	for _, want := range []string{`"instance_id"`, `"enabled"`, `"agents"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
	for _, forbid := range []string{`"ID"`, `"Name"`, `"Enabled"`} {
		if strings.Contains(body, forbid) {
			t.Fatalf("storage struct leaked into API: %s in %s", forbid, body)
		}
	}
}

func TestMetricsFilterByInstance(t *testing.T) {
	bucket := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	stub := &metricSourceStub{metrics: []aggregator.Metric{
		{InstanceID: "inst-a", BucketTime: bucket, DimensionType: "instance", DimensionKey: "inst-a", RequestCount: 7},
		{InstanceID: "inst-b", BucketTime: bucket, DimensionType: "instance", DimensionKey: "inst-b", RequestCount: 9},
	}}
	h := NewHandler(nil).WithMetricSource(stub)
	w := httptest.NewRecorder()
	h.HandleMetrics(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/metrics?instance_id=inst-a", nil))
	body := w.Body.String()
	if !strings.Contains(body, "inst-a") || strings.Contains(body, "inst-b") {
		t.Fatalf("instance filter leaked cross-instance metrics: %s", body)
	}
}

func TestRuntimeQueriesFilterByInstance(t *testing.T) {
	s := ingest.NewMemoryStore()
	now := time.Now().UTC()
	_ = s.UpsertAgent(storage.Agent{ID: "agent-a", InstanceID: "inst-a", LastSeenAt: now, Status: "online"})
	_ = s.UpsertAgent(storage.Agent{ID: "agent-b", InstanceID: "inst-b", LastSeenAt: now, Status: "online"})
	_ = s.InsertServerMetric(storage.ServerMetric{InstanceID: "inst-a", CollectedAt: now, CPUPercent: 10})
	_ = s.InsertServerMetric(storage.ServerMetric{InstanceID: "inst-b", CollectedAt: now, CPUPercent: 20})
	h := NewHandler(nil).WithRuntimeStore(s)

	w := httptest.NewRecorder()
	h.HandleAgents(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/agents?instance_id=inst-a", nil))
	if body := w.Body.String(); !strings.Contains(body, "agent-a") || strings.Contains(body, "agent-b") {
		t.Fatalf("agents filter leaked: %s", body)
	}

	w = httptest.NewRecorder()
	h.HandleServerMetrics(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/server-metrics?instance_id=inst-b", nil))
	if body := w.Body.String(); !strings.Contains(body, "inst-b") || strings.Contains(body, "inst-a") {
		t.Fatalf("server metrics filter leaked: %s", body)
	}
}
