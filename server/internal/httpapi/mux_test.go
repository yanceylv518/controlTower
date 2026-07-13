package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/aggregator"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
)

func TestWebAppReturnsServiceUnavailableWhenNotBuilt(t *testing.T) {
	mux := NewMux(Options{AgentToken: "agent", DashboardToken: "dash", Store: newTestStore(), WebAppDir: filepath.Join(t.TempDir(), "missing")})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "webapp_not_built") || !strings.Contains(response.Body.String(), "pnpm build") {
		t.Fatalf("webapp missing status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestWebAppServesIndexAndSPAFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("next-app"), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := NewMux(Options{AgentToken: "agent", DashboardToken: "dash", Store: newTestStore(), WebAppDir: dir})
	for _, path := range []string{"/", "/any/spa/route", "/assets/legacy.js"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || response.Body.String() != "next-app" {
			t.Fatalf("%s status=%d body=%q", path, response.Code, response.Body.String())
		}
	}
}

func TestNextWebRedirectsToRootPath(t *testing.T) {
	mux := NewMux(Options{AgentToken: "agent", DashboardToken: "dash", Store: newTestStore()})
	for _, tc := range []struct{ from, to string }{{"/next", "/"}, {"/next/alerts?active=1", "/alerts?active=1"}} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.from, nil))
		if response.Code != http.StatusMovedPermanently || response.Header().Get("Location") != tc.to {
			t.Fatalf("%s -> status=%d location=%q", tc.from, response.Code, response.Header().Get("Location"))
		}
	}
}

func TestWebAppDoesNotCaptureAPI(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("next-app"), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := NewMux(Options{AgentToken: "agent", DashboardToken: "dash", Store: newTestStore(), WebAppDir: dir})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/dashboard/overview", nil))
	if response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), "next-app") {
		t.Fatalf("api captured status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestWebAppDoesNotCaptureAPIRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("next-app"), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := NewMux(Options{AgentToken: "agent", DashboardToken: "dash", Store: newTestStore(), WebAppDir: dir})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api", nil))
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "next-app") {
		t.Fatalf("api root captured status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNewMuxExposesHealthz(t *testing.T) {
	mux := NewMux(Options{AgentToken: "agent-token", DashboardToken: "dashboard-token", Store: newTestStore()})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestNewMuxProtectsDashboardRoutes(t *testing.T) {
	mux := NewMux(Options{AgentToken: "agent-token", DashboardToken: "dashboard-token", Store: newTestStore()})
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/overview", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("dashboard without token status = %d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/dashboard/overview", nil)
	req.Header.Set("Authorization", "Bearer dashboard-token")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("dashboard with token status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestNewMuxProtectsAgentRoutes(t *testing.T) {
	mux := NewMux(Options{AgentToken: "agent-token", DashboardToken: "dashboard-token", Store: newTestStore()})
	req := httptest.NewRequest(http.MethodPost, "/api/agent/heartbeat", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("agent without token status = %d body=%s", rr.Code, rr.Body.String())
	}
}

type testStore struct {
	*ingest.MemoryStore
	metrics []aggregator.Metric
}

func newTestStore() *testStore {
	return &testStore{
		MemoryStore: ingest.NewMemoryStore(),
		metrics: []aggregator.Metric{{
			InstanceID:    "inst-1",
			BucketTime:    time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC),
			DimensionType: "instance",
			DimensionKey:  "inst-1",
			RequestCount:  1,
		}},
	}
}

func (s *testStore) Recent1mMetrics() ([]aggregator.Metric, error) {
	return append([]aggregator.Metric(nil), s.metrics...), nil
}

func (s *testStore) QueryLogEvents(query storage.LogQuery) ([]storage.LogEvent, error) {
	return s.MemoryStore.QueryLogEvents(query)
}
func TestNewMuxProtectsRuntimeDashboardRoutes(t *testing.T) {
	mux := NewMux(Options{AgentToken: "agent-token", DashboardToken: "dashboard-token", Store: newTestStore()})
	for _, path := range []string{"/api/dashboard/metrics", "/api/dashboard/metric-history?dimension_type=instance&dimension_key=inst-1", "/api/dashboard/usage", "/api/dashboard/channel-snapshots", "/api/dashboard/alerts", "/api/dashboard/notification-channels", "/api/dashboard/notification-deliveries", "/api/dashboard/server-metrics", "/api/dashboard/health-checks", "/api/dashboard/docker-statuses"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s without token status = %d body=%s", path, rr.Code, rr.Body.String())
		}

		req = httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer dashboard-token")
		rr = httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s with token status = %d body=%s", path, rr.Code, rr.Body.String())
		}
	}
}

func (s *testStore) Recent5mMetrics() ([]aggregator.Metric, error) {
	return append([]aggregator.Metric(nil), s.metrics...), nil
}

func TestNewMuxProtectsAlertActionRoute(t *testing.T) {
	mux := NewMux(Options{AgentToken: "agent-token", DashboardToken: "dashboard-token", Store: newTestStore()})
	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/alerts/action", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("alert action without token status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMuxRegistersAuthRoutes(t *testing.T) {
	mux := NewMux(Options{AgentToken: "agent", DashboardToken: "dash", Store: ingest.NewMemoryStore()})
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code == http.StatusNotFound {
		t.Fatalf("auth login route missing: %d", response.Code)
	}
}

func TestMuxRegistersInstanceRoutes(t *testing.T) {
	mux := NewMux(Options{AgentToken: "agent", DashboardToken: "dash", Store: ingest.NewMemoryStore()})
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/dashboard/instances"},
		{http.MethodPost, "/api/dashboard/instances"},
		{http.MethodPut, "/api/dashboard/instances/inst-x"},
		{http.MethodPost, "/api/dashboard/instances/inst-x/rotate-token"},
	} {
		request := httptest.NewRequest(tc.method, tc.path, nil)
		request.Header.Set("Authorization", "Bearer dash")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code == http.StatusNotFound && tc.path == "/api/dashboard/instances" {
			t.Fatalf("route missing: %s %s -> %d", tc.method, tc.path, response.Code)
		}
		if response.Code == http.StatusMethodNotAllowed && tc.method == http.MethodGet {
			t.Fatalf("method wiring wrong: %s %s", tc.method, tc.path)
		}
	}
}
