package dashboard

import (
	"context"
	cl "controltower/internal/containerlog"
	"controltower/server/internal/auth"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type failingLogRead struct {
	testLogStore
	err error
}

type reusableLogStore struct {
	testLogStore
	cached     cl.Task
	cacheActor int64
}

func (s *reusableLogStore) FindReusableContainerLog(_ context.Context, _, _ string, actor int64, _ cl.Query) (cl.Task, error) {
	s.cacheActor = actor
	return s.cached, nil
}

func TestContainerLogRepeatedQueryDoesNotCreateAgentTask(t *testing.T) {
	users := ingest.NewMemoryStore()
	hash, err := auth.HashPassword("test-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = users.CreateUser(storage.User{Username: "admin", Role: "admin", Permissions: []string{"*"}, Enabled: true, PasswordHash: hash}); err != nil {
		t.Fatal(err)
	}
	manager := auth.NewManager(users, time.Hour)
	u, session, err := manager.Login("admin", "test-password", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	store := &reusableLogStore{cached: cl.Task{ID: "cached-task", Result: cl.Result{Status: "succeeded", Complete: true, Lines: []string{"existing"}}}}
	now := time.Now().UTC()
	body := fmt.Sprintf(`{"instance_id":"site-node","agent_id":"agent","query":{"source_id":"%s","container":"new-api","from":"%s","to":"%s"}}`, strings.Repeat("a", 64), now.Add(-time.Hour).Format(time.RFC3339), now.Add(-time.Minute).Format(time.RFC3339))
	r := httptest.NewRequest("POST", "/api/dashboard/container-log-tasks", strings.NewReader(body))
	r.AddCookie(&http.Cookie{Name: "ct_session", Value: session.ID})
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	auth.RequireSessionOrToken(manager, "legacy", ContainerLogHandler{Store: store}).ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "cached-task") || store.created.ID != "" || store.cacheActor != u.ID {
		t.Fatalf("repeated query created work: %d %s %+v", w.Code, w.Body.String(), store)
	}
}

func (s *failingLogRead) GetContainerLog(context.Context, string, int64) (cl.Task, error) {
	return cl.Task{ID: "task"}, s.err
}
func TestContainerLogReadFailureThenRecovery(t *testing.T) {
	users := ingest.NewMemoryStore()
	hash, err := auth.HashPassword("test-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = users.CreateUser(storage.User{Username: "admin", Role: "admin", Permissions: []string{"*"}, Enabled: true, PasswordHash: hash}); err != nil {
		t.Fatal(err)
	}
	manager := auth.NewManager(users, time.Hour)
	_, session, err := manager.Login("admin", "test-password", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	store := &failingLogRead{}
	handler := auth.RequireSessionOrToken(manager, "legacy", ContainerLogHandler{Store: store})
	for _, tc := range []struct {
		err    error
		status int
	}{
		{nil, 200}, {errors.New("database lock conflict private-detail"), 500}, {nil, 200},
		{fmt.Errorf("query: %w", sql.ErrNoRows), 404}, {errors.New("invalid result JSON private-detail"), 500},
	} {
		store.err = tc.err
		r := httptest.NewRequest("GET", "/api/dashboard/container-log-tasks/task", nil)
		r.SetPathValue("id", "task")
		r.AddCookie(&http.Cookie{Name: "ct_session", Value: session.ID})
		r.Header.Set("X-Requested-With", "XMLHttpRequest")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private-detail") {
			t.Fatal("internal detail leaked")
		}
	}
}
