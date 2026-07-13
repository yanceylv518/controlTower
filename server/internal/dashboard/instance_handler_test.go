package dashboard

import (
	"bytes"
	"controltower/server/internal/ingest"
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
