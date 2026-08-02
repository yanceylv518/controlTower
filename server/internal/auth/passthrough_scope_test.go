package auth

import (
	"controltower/server/internal/storage"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestViewerPassthroughUsesStoredScopeInsteadOfRequestedScope(t *testing.T) {
	m, store := setup(t)
	hash, err := HashPassword("password1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err = store.CreateUser(storage.User{Username: "viewer", PasswordHash: hash, Role: "viewer", ScopeSite: "allowed-site", ScopeUserIDs: []int64{11, 12}, Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	_, session, err := m.Login("viewer", "password1", now)
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := CurrentUser(r)
		if !ok || user.ScopeSite != "allowed-site" || !reflect.DeepEqual(user.ScopeUserIDs, []int64{11, 12}) {
			t.Fatalf("unexpected stored scope: %#v, %v", user, ok)
		}
		if got := r.URL.Query().Get("site"); got != "allowed-site" {
			t.Fatalf("site query = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	r := httptest.NewRequest("GET", "/api/dashboard/passthrough/logs?site=other&user_ids=999", nil)
	r.AddCookie(&http.Cookie{Name: "ct_session", Value: session.ID})
	w := httptest.NewRecorder()
	RequireSessionOrToken(m, "legacy", next).ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}
