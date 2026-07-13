package agentgateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type tokenLookupStub struct {
	hash, id string
	expires  time.Time
	enabled  bool
}

func (s *tokenLookupStub) InstanceIDByTokenHash(h string, n time.Time) (string, bool, error) {
	return s.id, h == s.hash && s.enabled && (s.expires.IsZero() || s.expires.After(n)), nil
}

func TestInstanceTokenAuthAndMismatch(t *testing.T) {
	n := time.Now()
	token := "secret"
	s := &tokenLookupStub{hash: hashToken("pep", token), id: "inst-a", enabled: true}
	h := NewHandlerWithTokens("legacy", &memorySink{}, s, "pep")
	request := func(tok, id string, body []byte) int {
		r := httptest.NewRequest(http.MethodPost, "/api/agent/heartbeat", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		h.HandleHeartbeat(w, r)
		return w.Code
	}
	valid := []byte(`{"instance_id":"inst-a","agent_id":"a"}`)
	if got := request(token, "inst-a", valid); got != 200 {
		t.Fatal(got)
	}
	if got := request(token, "x", []byte(`{"instance_id":"inst-b","agent_id":"a"}`)); got != 403 {
		t.Fatal(got)
	}
	if got := request("legacy", "inst-a", valid); got != 200 {
		t.Fatal(got)
	}
	if got := request("wrong", "inst-a", []byte("not-gzip-or-json")); got != 401 {
		t.Fatalf("invalid token parsed body first: %d", got)
	}
	g := n.Add(time.Hour)
	s.expires = g
	if got := request(token, "inst-a", valid); got != 200 {
		t.Fatal(got)
	}
	s.expires = time.Now().Add(-time.Second)
	if got := request(token, "inst-a", valid); got != 401 {
		t.Fatal(got)
	}
	s.enabled = false
	s.expires = time.Time{}
	if got := request(token, "inst-a", valid); got != 401 {
		t.Fatal(got)
	}
}
