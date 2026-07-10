package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireBearerTokenRejectsMissingToken(t *testing.T) {
	handler := RequireBearerToken("dashboard-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/overview", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestRequireBearerTokenRejectsWrongToken(t *testing.T) {
	handler := RequireBearerToken("dashboard-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/overview", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestRequireBearerTokenRejectsEmptyExpectedToken(t *testing.T) {
	handler := RequireBearerToken("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/overview", nil)
	req.Header.Set("Authorization", "Bearer dashboard-token")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestRequireBearerTokenAllowsValidToken(t *testing.T) {
	called := false
	handler := RequireBearerToken("dashboard-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/overview", nil)
	req.Header.Set("Authorization", "Bearer dashboard-token")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !called {
		t.Fatal("next handler was not called")
	}
}

