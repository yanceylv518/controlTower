package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMuxRegistersProtectedCommandRoutes(t *testing.T) {
	mux := NewMux(Options{AgentToken: "agent", DashboardToken: "dash", Store: newTestStore()})
	for _, path := range []string{"/api/dashboard/channel-commands", "/api/dashboard/operation-audits"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s=%d", path, w.Code)
		}
	}
}
