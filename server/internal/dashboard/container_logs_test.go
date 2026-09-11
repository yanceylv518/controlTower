package dashboard

import (
	"context"
	cl "controltower/internal/containerlog"
	"controltower/server/internal/auth"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testLogStore struct {
	history  cl.HistoryFilter
	previous *cl.Task
	created  cl.Task
	reader   int64
}

func (s *testLogStore) ListContainerLogHistory(_ context.Context, f cl.HistoryFilter) (cl.HistoryPage, error) {
	s.history = f
	return cl.HistoryPage{Items: []cl.HistoryGroup{}, Page: f.Page, PageSize: f.PageSize}, nil
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
	if s.previous != nil && s.previous.ID == taskID && (id == 0 || s.previous.ActorID == id) {
		return *s.previous, nil
	}
	return cl.Task{}, sql.ErrNoRows
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
		{Username: "super", Role: "admin", Permissions: []string{"*"}, Enabled: true, PasswordHash: hash},
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
	for _, name := range []string{"super", "allowed", "denied", "viewer"} {
		u, session, e := manager.Login(name, "test-password", now)
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest("GET", "/api/dashboard/container-log-tasks", nil)
		r.AddCookie(&http.Cookie{Name: "ct_session", Value: session.ID})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		expected := 403
		if name == "allowed" || name == "super" {
			expected = 200
		}
		if w.Code != expected {
			t.Fatalf("%s: %d", name, w.Code)
		}
		if name == "allowed" && store.reader != u.ID {
			t.Fatal("restricted administrator history must remain private")
		}
		if name == "super" && store.reader != 0 {
			t.Fatal("full administrator must have all-actors history scope")
		}
		store.previous = &cl.Task{ID: "other-query", ActorID: 999, Actor: "other-user"}
		detail := httptest.NewRequest("GET", "/api/dashboard/container-log-tasks/other-query", nil)
		detail.SetPathValue("id", "other-query")
		detail.AddCookie(&http.Cookie{Name: "ct_session", Value: session.ID})
		detailResponse := httptest.NewRecorder()
		handler.ServeHTTP(detailResponse, detail)
		detailExpected := expected
		if name == "allowed" {
			detailExpected = 404
		}
		if detailResponse.Code != detailExpected {
			t.Fatalf("%s other-user detail: %d", name, detailResponse.Code)
		}
		for _, suffix := range []string{"?paged=1&site=test", "?paged=1&actor=other-user", "?paged=1&page_size=101"} {
			request := httptest.NewRequest("GET", "/api/dashboard/container-log-tasks"+suffix, nil)
			request.AddCookie(&http.Cookie{Name: "ct_session", Value: session.ID})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			want := expected
			if expected == 200 && strings.Contains(suffix, "actor=") && name != "super" {
				want = 403
			}
			if expected == 200 && strings.Contains(suffix, "101") {
				want = 400
			}
			if response.Code != want {
				t.Fatalf("%s %s: %d want %d", name, suffix, response.Code, want)
			}
			if response.Code == 200 {
				if store.history.Page != 1 || store.history.PageSize != 20 {
					t.Fatal("incorrect defaults")
				}
				if name == "allowed" && store.history.ActorID != u.ID {
					t.Fatal("paged history escaped owner scope")
				}
				if name == "super" && store.history.ActorID != 0 {
					t.Fatal("super history scope")
				}
			}
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
