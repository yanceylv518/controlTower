package dashboard

import (
	"context"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func menuRequest(t *testing.T, permission, method, path string, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	store := ingest.NewMemoryStore()
	if err := store.CreateUser(storage.User{ID: 1, Username: "operator", Role: "admin", Permissions: []string{permission}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(storage.Session{ID: "test-session", UserID: 1, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(&http.Cookie{Name: "ct_session", Value: "test-session"})
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	rec := httptest.NewRecorder()
	ctauth.RequireSessionOrToken(ctauth.NewManager(store, time.Hour), "", handler).ServeHTTP(rec, req)
	return rec
}

type menuResultStore struct {
	BillingStatementResultStore
	jobType string
	deleted bool
}

func (s *menuResultStore) BillingJob(context.Context, string) (billing.Job, error) {
	return billing.Job{ID: "job", JobType: s.jobType, Status: "complete"}, nil
}
func (s *menuResultStore) DeleteBillingStatement(context.Context, string) ([]string, error) {
	s.deleted = true
	return nil, nil
}

func TestBillingDeleteUsesStoredTypeInsteadOfCallerType(t *testing.T) {
	for _, tc := range []struct {
		permission, jobType string
		allowed             bool
	}{
		{"billing.users", "user_statement", true}, {"billing.users", "upstream_statement", false},
		{"billing.channels", "user_statement", false}, {"billing.channels", "upstream_statement", true},
		{"billing.tasks", "upstream_statement", true},
	} {
		s := &menuResultStore{jobType: tc.jobType}
		rec := menuRequest(t, tc.permission, "DELETE", "/api/dashboard/billing/statements/result?id=job&statement_type=user", BillingStatementResultHandler{Store: s})
		if s.deleted != tc.allowed || (tc.allowed && rec.Code != 200) || (!tc.allowed && rec.Code != 403) {
			t.Fatalf("%+v status=%d deleted=%v", tc, rec.Code, s.deleted)
		}
	}
}

type menuJobsStore struct{ BillingJobsStore }

func (s menuJobsStore) ListBillingJobs(context.Context, string, string, int) ([]billing.Job, error) {
	return []billing.Job{{ID: "u", JobType: "user_statement"}, {ID: "c", JobType: "upstream_statement"}}, nil
}

func TestBillingListFiltersUnauthorizedStatementTypes(t *testing.T) {
	for _, tc := range []struct {
		permission string
		count      int
		first      string
	}{{"billing.users", 1, "u"}, {"billing.channels", 1, "c"}, {"billing.tasks", 2, "u"}} {
		rec := menuRequest(t, tc.permission, "GET", "/api/dashboard/billing/jobs", BillingJobsHandler{Store: menuJobsStore{}})
		var body struct {
			Items []billing.Job `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if rec.Code != 200 || len(body.Items) != tc.count || body.Items[0].ID != tc.first {
			t.Fatalf("%+v status=%d body=%s", tc, rec.Code, rec.Body.String())
		}
	}
}
