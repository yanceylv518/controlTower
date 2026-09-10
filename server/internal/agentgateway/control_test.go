package agentgateway

import (
	"bytes"
	cp "controltower/internal/controlpoll"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestControlAuthAndSectionIsolation(t *testing.T) {
	s := &tokenLookupStub{hash: hashToken("pep", "secret"), id: "inst-a", enabled: true}
	h := NewHandlerWithTokens("legacy", &memorySink{}, s, "pep")
	calls := 0
	endpoint := h.Control(map[string]http.HandlerFunc{
		"archive": func(w http.ResponseWriter, r *http.Request) { calls++; writeError(w, 500, "archive_failed") },
		"container_logs": func(w http.ResponseWriter, r *http.Request) {
			calls++
			var p map[string]string
			if decodeJSON(w, r, &p) != nil || p["agent_id"] != "a" {
				t.Error("section payload changed")
			}
			writeJSON(w, 200, map[string]any{"task": nil})
		},
	})
	for _, token := range []string{"legacy", "wrong", "secret"} {
		r := httptest.NewRequest("POST", "/api/agent/control/poll", bytes.NewBufferString(`{"archive":{},"container_logs":{"agent_id":"a"}}`))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		endpoint(w, r)
		if token != "secret" {
			if w.Code != 401 || calls != 0 {
				t.Fatal("unauthorized dispatch", w.Code, calls)
			}
			continue
		}
		var out cp.Response
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || w.Code != 200 || calls != 2 || out["archive"].Status != 500 || out["container_logs"].Status != 200 {
			t.Fatal("section failure affected sibling", w.Body.String())
		}
	}
}
