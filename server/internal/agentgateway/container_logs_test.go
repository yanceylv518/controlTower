package agentgateway

import (
	"context"
	cl "controltower/internal/containerlog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type logLookup struct{}

func (logLookup) InstanceIDByTokenHash(string, time.Time) (string, bool, error) {
	return "instance-a", true, nil
}

type logPollStore struct {
	instance string
	calls    int
}

func (s *logPollStore) PollContainerLogs(_ context.Context, instance string, _ cl.Poll) (*cl.Task, error) {
	s.instance = instance
	s.calls++
	return nil, nil
}
func TestContainerLogPollRequiresScopedTokenAndBounds(t *testing.T) {
	s := &logPollStore{}
	h := NewHandler("global-token", nil)
	r := httptest.NewRequest("POST", "/api/agent/container-logs/poll", strings.NewReader(`{"agent_id":"a","sources":[{"container":"new-api","id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","available":true}]}`))
	r.Header.Set("Authorization", "Bearer global-token")
	w := httptest.NewRecorder()
	h.ContainerLogs(s)(w, r)
	if w.Code != 401 || s.calls != 0 {
		t.Fatal("legacy token allowed", w.Code)
	}
	h = NewHandlerWithTokens("", nil, logLookup{}, "pepper")
	r = httptest.NewRequest("POST", "/api/agent/container-logs/poll", strings.NewReader(`{"agent_id":"a","sources":[{"container":"new-api","id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","available":true}]}`))
	r.Header.Set("Authorization", "Bearer scoped")
	w = httptest.NewRecorder()
	h.ContainerLogs(s)(w, r)
	if w.Code != 200 || s.instance != "instance-a" {
		t.Fatal("token scope not used", w.Code)
	}
	r = httptest.NewRequest("POST", "/api/agent/container-logs/poll", strings.NewReader(`{"agent_id":"a","sources":[{"container":"--help"}]}`))
	r.Header.Set("Authorization", "Bearer scoped")
	w = httptest.NewRecorder()
	h.ContainerLogs(s)(w, r)
	if w.Code != 400 || s.calls != 1 {
		t.Fatal("invalid poll accepted")
	}
}
