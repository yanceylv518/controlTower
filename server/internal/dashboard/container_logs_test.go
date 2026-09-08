package dashboard

import (
	"context"
	cl "controltower/internal/containerlog"
	"controltower/server/internal/auth"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testLogStore struct {
	previous *cl.Task
	created  cl.Task
	reader   int64
}

func (s *testLogStore) ContainerLogTargets(context.Context) ([]cl.Target, error) {
	return []cl.Target{}, nil
}
func (s *testLogStore) CreateContainerLog(_ context.Context, t cl.Task) error {
	s.created = t
	return nil
}
func (s *testLogStore) ListContainerLogs(_ context.Context, id int64) ([]cl.Task, error) {
	s.reader = id
	return []cl.Task{}, nil
}
func (s *testLogStore) GetContainerLog(_ context.Context, taskID string, id int64) (cl.Task, error) {
	s.reader = id
	if s.previous != nil && s.previous.ID == taskID && s.previous.ActorID == id {
		return *s.previous, nil
	}
	return cl.Task{}, errors.New("not found")
}
func (s *testLogStore) PollContainerLogs(context.Context, string, cl.Poll) (*cl.Task, error) {
	return nil, nil
}
func TestContainerLogPermissionsAndSessionActor(t *testing.T) {
	users := ingest.NewMemoryStore()
	hash, e := auth.HashPassword("test-password")
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	for _, u := range []storage.User{
		{Username: "allowed", DisplayName: "Operator", Role: "admin", Permissions: []string{"logs.query"}, Enabled: true, PasswordHash: hash},
		{Username: "denied", Role: "admin", Permissions: []string{"data.logs"}, Enabled: true, PasswordHash: hash},
		{Username: "viewer", Role: "viewer", Enabled: true, PasswordHash: hash},
	} {
		if e = users.CreateUser(u); e != nil {
			t.Fatal(e)
		}
	}
	manager := auth.NewManager(users, time.Hour)
	store := &testLogStore{}
	handler := auth.RequireSessionOrToken(manager, "legacy", ContainerLogHandler{Store: store})
	for _, name := range []string{"allowed", "denied", "viewer"} {
		u, session, e := manager.Login(name, "test-password", now)
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest("GET", "/api/dashboard/container-log-tasks", nil)
		r.AddCookie(&http.Cookie{Name: "ct_session", Value: session.ID})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		expected := 403
		if name == "allowed" {
			expected = 200
		}
		if w.Code != expected {
			t.Fatalf("%s: %d", name, w.Code)
		}
		if name == "allowed" {
			body := `{"instance_id":"inst","agent_id":"agent","query":{"source_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","container":"new-api","from":"` + now.Add(-time.Minute).Format(time.RFC3339) + `","to":"` + now.Format(time.RFC3339) + `"}}`
			r = httptest.NewRequest("POST", "/api/dashboard/container-log-tasks", strings.NewReader(body))
			r.AddCookie(&http.Cookie{Name: "ct_session", Value: session.ID})
			r.Header.Set("X-Requested-With", "XMLHttpRequest")
			w = httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 202 || store.created.ActorID != u.ID || store.created.Actor != "allowed" {
				t.Fatal("actor not from session", w.Code)
			}
			request := httptest.NewRequest("POST", "/api/dashboard/container-log-tasks", strings.NewReader(strings.Replace(body, `"query":{`, `"query":{"cursor":"`+strings.Repeat("b", 64)+`",`, 1)))
			request.AddCookie(&http.Cookie{Name: "ct_session", Value: session.ID})
			request.Header.Set("X-Requested-With", "XMLHttpRequest")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != 400 {
				t.Fatal("browser submitted an internal cursor", response.Code)
			}

		}
	}
	r := httptest.NewRequest("GET", "/api/dashboard/container-log-tasks", nil)
	r.Header.Set("Authorization", "Bearer legacy")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("anonymous legacy token permitted log query")
	}
}
