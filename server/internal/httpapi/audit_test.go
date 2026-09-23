package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/auditmeta"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
)

func TestAuthenticationAuditLifecycleForAdminAndViewer(t *testing.T) {
	for _, role := range []string{"admin", "viewer"} {
		t.Run(role, func(t *testing.T) {
			store := ingest.NewMemoryStore()
			manager := ctauth.NewManager(store, time.Hour)
			hash, err := ctauth.HashPassword("valid-password")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.CreateUser(storage.User{Username: "audit-user", PasswordHash: hash, Role: role, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			h := ctauth.Handlers{M: manager, Audit: store}
			var cookie *http.Cookie
			call := func(path, body string, handler http.HandlerFunc, expected int) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				r.RemoteAddr = "203.0.113.22:1234"
				r.Header.Set("X-Requested-With", "XMLHttpRequest")
				if cookie != nil {
					r.AddCookie(cookie)
				}
				w := httptest.NewRecorder()
				auditMutations(handler, store).ServeHTTP(w, r)
				if w.Code != expected {
					t.Fatalf("%s status=%d body=%s", path, w.Code, w.Body.String())
				}
				return w
			}
			call("/api/auth/login", `{"username":"audit-user","password":"wrong-password"}`, h.Login, 401)
			login := call("/api/auth/login", `{"username":"audit-user","password":"valid-password"}`, h.Login, 200)
			cookie = login.Result().Cookies()[0]
			firstSession := cookie.Value
			call("/api/auth/password", `{"old_password":"wrong-password","new_password":"next-password"}`, h.Password, 401)
			call("/api/auth/password", `{"old_password":"valid-password","new_password":"next-password"}`, h.Password, 200)
			if _, ok := manager.Validate(firstSession, time.Now()); ok {
				t.Fatal("password change retained old session")
			}
			cookie = nil
			login = call("/api/auth/login", `{"username":"audit-user","password":"next-password"}`, h.Login, 200)
			cookie = login.Result().Cookies()[0]
			call("/api/auth/logout", "", h.Logout, 200)
			if _, ok := manager.Validate(cookie.Value, time.Now()); ok {
				t.Fatal("logout retained session")
			}
			page, err := store.QueryOperationAudits(storage.OperationAuditQuery{Limit: 20})
			if err != nil || page.Total != 6 {
				t.Fatalf("expected one audit per request: %+v err=%v", page, err)
			}
			counts := map[string]int{}
			for _, item := range page.Items {
				counts[item.OperationType]++
				if item.RequestID == "" || item.HTTPStatus == 0 || item.ClientIP != "203.0.113.22" || item.AuthMethod != "session" {
					t.Fatalf("missing trace: %+v", item)
				}
				if item.Status == "succeeded" && (item.ActorID != "audit-user" || item.ActorRole != role) {
					t.Fatalf("wrong attribution: %+v", item)
				}
				if item.OperationType == "auth.login" && item.Status == "failed" && (item.ActorID != "unknown" || item.TargetID != "audit-user") {
					t.Fatalf("failed login falsely attributed: %+v", item)
				}
				encoded, _ := json.Marshal(item)
				for _, secret := range []string{"valid-password", "wrong-password", "next-password", firstSession, cookie.Value} {
					if strings.Contains(string(encoded), secret) {
						t.Fatal("audit leaked credential")
					}
				}
			}
			if counts["auth.login"] != 3 || counts["auth.logout"] != 1 || counts["auth.password_change"] != 2 {
				t.Fatalf("operations=%v", counts)
			}
		})
	}
}

func TestAuditMutationsRedactsSecretsAndRecordsFailureContext(t *testing.T) {
	store := ingest.NewMemoryStore()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"update_failed"}`, http.StatusBadRequest)
	})
	handler := ctauth.RequireSessionOrToken(nil, "dashboard-secret", auditMutations(next, store))
	r := httptest.NewRequest(http.MethodPut, "/api/dashboard/instances/demo", strings.NewReader(`{"name":"production","password":"secret-value","control_api_token":"token-value","logs_readonly_dsn":"mysql://user:pass@host/db","apiKey":"camel-api-value","clientSecret":"camel-secret-value","privateKey":"private-key-value"}`))
	r.Pattern = "PUT /api/dashboard/instances/{id}"
	r.Header.Set("Authorization", "Bearer dashboard-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	page, err := store.QueryOperationAudits(storage.OperationAuditQuery{Status: "failed", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("audit rows=%+v", page)
	}
	item := page.Items[0]
	for _, secret := range []string{"secret-value", "token-value", "mysql://", "user:pass", "camel-api-value", "camel-secret-value", "private-key-value"} {
		if strings.Contains(item.AfterSummary, secret) {
			t.Fatalf("audit leaked %q: %s", secret, item.AfterSummary)
		}
	}
	if item.ActorType != "service_token" || item.AuthMethod != "bearer" || item.HTTPStatus != http.StatusBadRequest || item.TargetID != "demo" || item.Route != r.Pattern || w.Header().Get("X-Request-ID") == "" {
		t.Fatalf("audit context incomplete: %+v", item)
	}
}

func TestAuditMutationRecordsSessionActorAndRequestTrace(t *testing.T) {
	store := ingest.NewMemoryStore()
	manager := ctauth.NewManager(store, time.Hour)
	passwordHash, err := ctauth.HashPassword("valid-password")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.CreateUser(storage.User{Username: "audit-admin", PasswordHash: passwordHash, Role: "admin", Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	_, session, err := manager.Login("audit-admin", "valid-password", now)
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	handler := ctauth.RequireSessionOrToken(manager, "", auditMutations(next, store))
	r := httptest.NewRequest(http.MethodPut, "/api/dashboard/settings", strings.NewReader(`{"api_key":"must-not-appear","enabled":true}`))
	r.Pattern = "PUT /api/dashboard/settings"
	r.RemoteAddr = "203.0.113.11:4321"
	r.AddCookie(&http.Cookie{Name: "ct_session", Value: session.ID})
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusCreated || w.Body.String() != `{"ok":true}` {
		t.Fatalf("audited response changed: status=%d body=%s", w.Code, w.Body.String())
	}

	page, err := store.QueryOperationAudits(storage.OperationAuditQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("manual session mutation was not recorded exactly once: %+v", page)
	}
	item := page.Items[0]
	if item.ActorID != "audit-admin" || item.ActorType != "human" || item.ActorRole != "admin" || item.AuthMethod != "session" || item.HTTPMethod != http.MethodPut || item.Route != r.Pattern || item.ClientIP != "203.0.113.11" || item.HTTPStatus != http.StatusCreated || item.Status != "succeeded" {
		t.Fatalf("session operation metadata is inaccurate: %+v", item)
	}
	if item.RequestID == "" || item.CorrelationID != item.RequestID || w.Header().Get("X-Request-ID") != item.RequestID {
		t.Fatalf("request trace identifiers are inconsistent: audit=%+v header=%q", item, w.Header().Get("X-Request-ID"))
	}
	if strings.Contains(item.AfterSummary, "must-not-appear") || !strings.Contains(item.AfterSummary, "[redacted]") {
		t.Fatalf("sensitive request data was not redacted: %s", item.AfterSummary)
	}
}

func TestAuditMutationsSkipsReadOnlyPostAndLinksReturnedCommand(t *testing.T) {
	store := ingest.NewMemoryStore()
	readonly := auditMutations(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), store)
	r := httptest.NewRequest(http.MethodPost, "/api/dashboard/tuning/preflight", strings.NewReader(`{"instance_id":"site"}`))
	readonly.ServeHTTP(httptest.NewRecorder(), r)
	page, _ := store.QueryOperationAudits(storage.OperationAuditQuery{Limit: 20})
	if page.Total != 0 {
		t.Fatalf("read-only preflight was audited: %+v", page.Items)
	}

	command := auditMutations(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"command-42"}`))
	}), store)
	r = httptest.NewRequest(http.MethodPost, "/api/dashboard/channels/42/commands", strings.NewReader(`{"instance_id":"site","status":2}`))
	r.Pattern = "POST /api/dashboard/channels/{channelID}/commands"
	commandResponse := httptest.NewRecorder()
	command.ServeHTTP(commandResponse, r)
	if commandResponse.Code != http.StatusCreated || commandResponse.Body.String() != `{"id":"command-42"}` {
		t.Fatalf("audited response changed: status=%d body=%s", commandResponse.Code, commandResponse.Body.String())
	}
	page, _ = store.QueryOperationAudits(storage.OperationAuditQuery{Limit: 20})
	if page.Total != 1 || page.Items[0].CorrelationID != "command-42" || page.Items[0].TargetID != "42" {
		t.Fatalf("command submission is not traceable: %+v", page.Items)
	}
}

type failingOperationAuditStore struct{}

func (failingOperationAuditStore) InsertOperationAudit(storage.OperationAudit) error {
	return errors.New("database unavailable")
}

type failingAuditStatusStore struct {
	*ingest.MemoryStore
}

func (failingAuditStatusStore) UpdateOperationAuditHTTPStatus(string, int) error {
	return errors.New("database unavailable")
}

func TestAuditMutationWithholdsSuccessWhenAuditPersistenceFails(t *testing.T) {
	var actionRan bool
	handler := auditMutations(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		actionRan = true
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "ct_session=unexpected")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}), failingOperationAuditStore{})
	r := httptest.NewRequest(http.MethodPost, "/api/dashboard/commands", strings.NewReader(`{"action":"run"}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if !actionRan {
		t.Fatal("mutation handler was not called")
	}
	requestID := w.Header().Get("X-Request-ID")
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("success was exposed without an audit record: status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"error":"operation_result_unknown"`) || requestID == "" || !strings.Contains(w.Body.String(), requestID) {
		t.Fatalf("unknown result response lacks reconciliation ID: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "确认前不要重试") {
		t.Fatalf("unknown result response does not warn against blind retry: %s", w.Body.String())
	}
	if w.Header().Get("Set-Cookie") != "" {
		t.Fatal("response headers from the uncommitted success leaked")
	}
}

func TestAuditMutationReportsUnknownWhenSemanticAuditFailedAfterWrite(t *testing.T) {
	store := ingest.NewMemoryStore()
	handler := auditMutations(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"billing_discount_audit_failed"}`, http.StatusInternalServerError)
	}), store)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/dashboard/billing/discounts", strings.NewReader(`{"discount":0.9}`)))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"error":"operation_result_unknown"`) {
		t.Fatalf("semantic audit failure was exposed as an ordinary operation failure: status=%d body=%s", w.Code, w.Body.String())
	}
	page, err := store.QueryOperationAudits(storage.OperationAuditQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].ErrorSummary != "billing_discount_audit_failed" {
		t.Fatalf("fallback audit did not retain the semantic audit error: %+v", page.Items)
	}
}

func TestAuditMutationWithoutStoreDoesNotRunHandler(t *testing.T) {
	var actionRan bool
	handler := auditMutations(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		actionRan = true
	}), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/dashboard/settings", strings.NewReader(`{}`)))
	if actionRan || w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"error":"audit_unavailable"`) {
		t.Fatalf("mutation was not fail-closed without audit store: ran=%v status=%d body=%s", actionRan, w.Code, w.Body.String())
	}
}

func TestAuditMutationBoundsBufferedResponseAndKeepsRecordedStatus(t *testing.T) {
	store := ingest.NewMemoryStore()
	handler := auditMutations(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(make([]byte, auditMutationResponseLimit+1))
	}), store)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/dashboard/commands", strings.NewReader(`{}`)))
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), `"error":"operation_response_too_large"`) {
		t.Fatalf("oversized mutation response was not bounded: status=%d body=%s", w.Code, w.Body.String())
	}
	page, err := store.QueryOperationAudits(storage.OperationAuditQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].HTTPStatus != http.StatusCreated || page.Items[0].Status != "succeeded" {
		t.Fatalf("operation outcome was not recorded before response replacement: %+v", page.Items)
	}
}

func TestAuditMutationRecordsUncoveredConfigurationRouteAndActorContext(t *testing.T) {
	store := ingest.NewMemoryStore()
	handler := auditMutations(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auditmeta.SetActor(r, "admin-1", "human", "admin", "session")
		w.WriteHeader(http.StatusNoContent)
	}), store)
	r := httptest.NewRequest(http.MethodPut, "/api/dashboard/archive-datasets/logs/coverage-policy", strings.NewReader(`{"enabled":false}`))
	r.Pattern = "PUT /api/dashboard/archive-datasets/{dataset}/coverage-policy"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	page, err := store.QueryOperationAudits(storage.OperationAuditQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Fatalf("uncovered mutation was not audited: %+v", page.Items)
	}
	item := page.Items[0]
	if item.ActorID != "admin-1" || item.ActorType != "human" || item.ActorRole != "admin" || item.AuthMethod != "session" || item.Status != "succeeded" {
		t.Fatalf("actor context missing: %+v", item)
	}
}

func TestAuditMutationSkipsOnlyConfirmedSemanticAudit(t *testing.T) {
	store := ingest.NewMemoryStore()
	handler := auditMutations(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		audit := storage.OperationAudit{ID: "semantic", OperationType: "settings.update", ActorID: "admin", Status: "succeeded"}
		auditmeta.Enrich(r, &audit)
		if err := store.InsertOperationAudit(audit); err != nil {
			t.Errorf("insert semantic audit: %v", err)
		}
		auditmeta.MarkSemanticAudit(r)
		w.WriteHeader(http.StatusNoContent)
	}), store)
	r := httptest.NewRequest(http.MethodPut, "/api/dashboard/settings", strings.NewReader(`{"values":{"CT_CPU_WARN_PERCENT":"75"}}`))
	r.RemoteAddr = "203.0.113.9:4321"
	r.Pattern = "PUT /api/dashboard/settings"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("semantic audit response changed: %d", w.Code)
	}

	page, err := store.QueryOperationAudits(storage.OperationAuditQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].ID != "semantic" {
		t.Fatalf("semantic audit was duplicated: %+v", page.Items)
	}
	item := page.Items[0]
	if item.RequestID == "" || item.CorrelationID != item.RequestID || item.ClientIP != "203.0.113.9" || item.HTTPMethod != http.MethodPut || item.Route != r.Pattern || item.HTTPStatus != http.StatusNoContent {
		t.Fatalf("semantic audit did not retain its completed request context: %+v", item)
	}
}

func TestAuditMutationAllowsExplicitNoChangeWithoutAuditRow(t *testing.T) {
	store := ingest.NewMemoryStore()
	handler := auditMutations(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auditmeta.MarkAuditHandledWithoutRecord(r)
		w.WriteHeader(http.StatusNoContent)
	}), store)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/dashboard/tuning/policy", strings.NewReader(`{"mode":"observe"}`)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("explicit no-change response was altered: status=%d body=%s", w.Code, w.Body.String())
	}
	page, err := store.QueryOperationAudits(storage.OperationAuditQuery{Limit: 20})
	if err != nil || page.Total != 0 {
		t.Fatalf("no-op mutation must not create a change audit: page=%+v err=%v", page, err)
	}
}

func TestAuditMutationDoesNotReportSuccessWhenSemanticStatusCannotBeRecorded(t *testing.T) {
	store := failingAuditStatusStore{MemoryStore: ingest.NewMemoryStore()}
	handler := auditMutations(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		audit := storage.OperationAudit{ID: "semantic", OperationType: "settings.update", ActorID: "admin", Status: "succeeded"}
		auditmeta.Enrich(r, &audit)
		if err := store.InsertOperationAudit(audit); err != nil {
			t.Errorf("insert semantic audit: %v", err)
		}
		auditmeta.MarkSemanticAudit(r)
		w.WriteHeader(http.StatusNoContent)
	}), store)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/dashboard/settings", strings.NewReader(`{"enabled":true}`)))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"error":"operation_result_unknown"`) {
		t.Fatalf("success was exposed without a complete semantic audit: status=%d body=%s", w.Code, w.Body.String())
	}
}
