package dashboard

import (
	"context"
	ac "controltower/internal/archivecontrol"
	"controltower/server/internal/auth"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type archiveMonthStore struct {
	ac.Store
	latest, queried string
	lookups         int
}

func (s *archiveMonthStore) ListLogArchives(context.Context, string) ([]ac.Item, error) {
	return []ac.Item{{SiteID: "site"}}, nil
}
func (s *archiveMonthStore) LatestLogArchiveMonth(context.Context, string) (string, error) {
	s.lookups++
	return s.latest, nil
}
func (s *archiveMonthStore) ListLogArchiveDays(_ context.Context, _ string, month string) ([]ac.Day, error) {
	s.queried = month
	return []ac.Day{}, nil
}

func TestArchiveDefaultMonthAndExplicitSelection(t *testing.T) {
	users := ingest.NewMemoryStore()
	password, _ := auth.HashPassword("test-password")
	if err := users.CreateUser(storage.User{Username: "admin", Role: "admin", Permissions: []string{"archive.manage"}, Enabled: true, PasswordHash: password}); err != nil {
		t.Fatal(err)
	}
	manager := auth.NewManager(users, time.Hour)
	_, session, err := manager.Login("admin", "test-password", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	current := time.Now().In(time.FixedZone("Beijing", 28800)).Format("2006-01")
	for _, tc := range []struct {
		latest, query, want string
		lookups             int
	}{{"2026-06", "", "2026-06", 1}, {"2026-06", "&month=2026-05", "2026-05", 0}, {"", "", current, 1}} {
		s := &archiveMonthStore{latest: tc.latest}
		handler := auth.RequireSessionOrToken(manager, "", LogArchiveHandler{Store: s})
		r := httptest.NewRequest("GET", "/api/dashboard/log-archives?site_id=site"+tc.query, nil)
		r.AddCookie(&http.Cookie{Name: "ct_session", Value: session.ID})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var out struct {
			Month string `json:"month"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Month != tc.want || s.queried != tc.want || s.lookups != tc.lookups {
			t.Fatalf("case %+v: %d %s", tc, w.Code, w.Body.String())
		}
	}
}
