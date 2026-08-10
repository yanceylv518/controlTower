package dashboard

import (
	"bytes"
	"context"
	"controltower/server/internal/aggregator"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
	"encoding/json"
	"errors"
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
	h.Create(w, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"instance_id":"inst-dto","site_id":"site-a","name":"D"}`)))
	w = httptest.NewRecorder()
	h.List(w, httptest.NewRequest(http.MethodGet, "/", nil))
	body := w.Body.String()
	for _, want := range []string{`"instance_id"`, `"site_id":"site-a"`, `"enabled"`, `"agents"`} {
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

func TestInstanceSiteValidationAndFallback(t *testing.T) {
	s := ingest.NewMemoryStore()
	h := InstanceHandler{Store: s, Runtime: s, Pepper: "pep"}

	w := httptest.NewRecorder()
	h.Create(w, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"instance_id":"site_a_1","name":"A"}`)))
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"site_id":"site_a_1"`) {
		t.Fatalf("expected instance-id fallback, status=%d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	h.Create(w, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"instance_id":"site-a-2","site_id":"BAD SITE"}`)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_site_id") {
		t.Fatalf("expected site validation error, status=%d body=%s", w.Code, w.Body.String())
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

func TestMetricsFilterBySiteAndInstancePrecedence(t *testing.T) {
	bucket := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	store := ingest.NewMemoryStore()
	for _, instance := range []storage.Instance{
		{ID: "site-a-1", SiteID: "site-a"},
		{ID: "site-a-2", SiteID: "site-a"},
		{ID: "site-b-1", SiteID: "site-b"},
	} {
		_ = store.CreateInstance(instance)
	}
	stub := &metricSourceStub{metrics: []aggregator.Metric{
		{InstanceID: "site-a-1", BucketTime: bucket, DimensionType: "instance", DimensionKey: "site-a-1", RequestCount: 7},
		{InstanceID: "site-a-2", BucketTime: bucket, DimensionType: "instance", DimensionKey: "site-a-2", RequestCount: 8},
		{InstanceID: "site-b-1", BucketTime: bucket, DimensionType: "instance", DimensionKey: "site-b-1", RequestCount: 9},
	}}
	h := NewHandler(nil).WithMetricSource(stub).WithInstanceStore(store)

	w := httptest.NewRecorder()
	h.HandleMetrics(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/metrics?site=site-a", nil))
	if body := w.Body.String(); !strings.Contains(body, "site-a-1") || !strings.Contains(body, "site-a-2") || strings.Contains(body, "site-b-1") {
		t.Fatalf("site filter leaked: %s", body)
	}

	w = httptest.NewRecorder()
	h.HandleMetrics(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/metrics?site=site-a&instance_id=site-a-2", nil))
	if body := w.Body.String(); strings.Contains(body, "site-a-1") || !strings.Contains(body, "site-a-2") {
		t.Fatalf("instance precedence failed: %s", body)
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

type controlConfigStub struct {
	saved map[string]storage.SiteControlConfig
}

func (c *controlConfigStub) ControlConfigForSite(siteID string) (storage.SiteControlConfig, error) {
	return c.saved[siteID], nil
}
func (c *controlConfigStub) UpdateControlConfigForSite(siteID, apiURL, encryptedToken string, adminUserID int64, _ time.Time) error {
	c.saved[siteID] = storage.SiteControlConfig{APIURL: apiURL, EncryptedToken: encryptedToken, AdminUserID: adminUserID}
	return nil
}

func TestInstanceControlConfigSaveValidatesAndEncrypts(t *testing.T) {
	s := ingest.NewMemoryStore()
	stub := &controlConfigStub{saved: map[string]storage.SiteControlConfig{}}
	var checkedURL, checkedToken string
	var checkedUser int64
	h := InstanceHandler{Store: s, Runtime: s, Pepper: "pep", ControlConfig: stub, SecretKey: "secret-key",
		ControlCheck: func(_ context.Context, apiURL, accessToken string, adminUserID int64) error {
			checkedURL, checkedToken, checkedUser = apiURL, accessToken, adminUserID
			return nil
		}}
	w := httptest.NewRecorder()
	h.Create(w, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"instance_id":"inst-a","site_id":"site-a","name":"A"}`)))
	if w.Code != 201 {
		t.Fatal(w.Code)
	}

	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(`{"control_api_url":"https://alb.example.com","control_api_token":"tok-1","control_admin_user_id":7}`))
	r.SetPathValue("id", "inst-a")
	h.Update(w, r)
	if w.Code != 200 {
		t.Fatalf("save control config: %d %s", w.Code, w.Body.String())
	}
	if checkedURL != "https://alb.example.com" || checkedToken != "tok-1" || checkedUser != 7 {
		t.Fatalf("live check not called with saved values: %q %q %d", checkedURL, checkedToken, checkedUser)
	}
	cfg := stub.saved["site-a"]
	if cfg.APIURL != "https://alb.example.com" || cfg.AdminUserID != 7 {
		t.Fatalf("config not stored site-wide: %#v", cfg)
	}
	if cfg.EncryptedToken == "tok-1" || cfg.EncryptedToken == "" {
		t.Fatalf("token must be stored encrypted: %q", cfg.EncryptedToken)
	}
	if plain, err := decryptSecret("secret-key", cfg.EncryptedToken); err != nil || plain != "tok-1" {
		t.Fatalf("token round-trip failed: %q %v", plain, err)
	}
	if !strings.Contains(w.Body.String(), `"control_configured":true`) || strings.Contains(w.Body.String(), "tok-1") {
		t.Fatalf("response must expose configured flag but never the token: %s", w.Body.String())
	}
}

func TestInstanceControlConfigRejectsInvalidAndFailedCheck(t *testing.T) {
	s := ingest.NewMemoryStore()
	stub := &controlConfigStub{saved: map[string]storage.SiteControlConfig{}}
	h := InstanceHandler{Store: s, Runtime: s, Pepper: "pep", ControlConfig: stub, SecretKey: "secret-key",
		ControlCheck: func(context.Context, string, string, int64) error { return errors.New("boom") }}
	w := httptest.NewRecorder()
	h.Create(w, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"instance_id":"inst-a","site_id":"site-a","name":"A"}`)))

	for _, body := range []string{
		`{"control_api_url":"ftp://x","control_api_token":"t","control_admin_user_id":7}`,
		`{"control_api_url":"https://alb","control_api_token":"","control_admin_user_id":7}`,
		`{"control_api_url":"https://alb","control_api_token":"t"}`,
	} {
		w = httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(body))
		r.SetPathValue("id", "inst-a")
		h.Update(w, r)
		if w.Code != 400 {
			t.Fatalf("invalid config must 400: %s -> %d", body, w.Code)
		}
	}

	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(`{"control_api_url":"https://alb","control_api_token":"t","control_admin_user_id":7}`))
	r.SetPathValue("id", "inst-a")
	h.Update(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "control_connection_test_failed") {
		t.Fatalf("failed live check must block save: %d %s", w.Code, w.Body.String())
	}
	if len(stub.saved) != 0 {
		t.Fatalf("nothing may be stored on failure: %#v", stub.saved)
	}

	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(`{"control_api_url":""}`))
	r.SetPathValue("id", "inst-a")
	h.Update(w, r)
	if w.Code != 200 {
		t.Fatalf("clearing must not require a live check: %d %s", w.Code, w.Body.String())
	}
	if cfg := stub.saved["site-a"]; cfg.APIURL != "" || cfg.EncryptedToken != "" || cfg.AdminUserID != 0 {
		t.Fatalf("clear must wipe all three fields: %#v", cfg)
	}
}
