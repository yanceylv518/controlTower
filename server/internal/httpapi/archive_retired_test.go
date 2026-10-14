package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ac "controltower/internal/archivecontrol"
	aj "controltower/internal/archivejob"
	"controltower/server/internal/auth"
	"controltower/server/internal/dashboard"
	"controltower/server/internal/storage"
)

type retiredArchiveFixture struct {
	*testStore
	dashboard.ArchiveBackfillStore // nil mutations panic if routing regresses
	dashboard.ArchiveVerifyStore
	reads  int
	writes int
}

func (s *retiredArchiveFixture) ListArchiveBackfills(context.Context, string, string) ([]ac.BackfillTaskItem, error) {
	s.reads++
	return []ac.BackfillTaskItem{}, nil
}
func (s *retiredArchiveFixture) ListArchiveReconciles(context.Context, string, string) ([]ac.ReconcileTaskItem, error) {
	s.reads++
	return []ac.ReconcileTaskItem{}, nil
}
func (s *retiredArchiveFixture) ListArchiveSeals(context.Context, string, string) ([]ac.SealTaskItem, error) {
	s.reads++
	return []ac.SealTaskItem{}, nil
}

type currentArchiveFixture struct {
	ac.Store
	owner *retiredArchiveFixture
}

func (s currentArchiveFixture) ListLogArchives(context.Context, string) ([]ac.Item, error) {
	return []ac.Item{}, nil
}
func (s currentArchiveFixture) UpdateLogArchive(context.Context, string, ac.Config, string) error {
	s.owner.writes++
	return nil
}
func (s *retiredArchiveFixture) NewArchiveStore() ac.Store { return currentArchiveFixture{owner: s} }

func TestMuxRetiresLegacyArchiveTaskMutationsButPreservesReadsAndNewJobs(t *testing.T) {
	s := &retiredArchiveFixture{testStore: newTestStore()}
	now := time.Now().UTC()
	for _, v := range []struct {
		name        string
		permissions []string
	}{{"archive", []string{"archive.manage"}}, {"other", []string{"audits.read"}}} {
		if err := s.CreateUser(storage.User{Username: v.name, Role: "admin", Permissions: v.permissions, Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		u, _, _ := s.UserByUsername(v.name)
		if err := s.CreateSession(storage.Session{ID: v.name, UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	mux := NewMux(Options{Store: s, AuthManager: auth.NewManager(s, time.Hour), AgentToken: "agent"})
	base := "/api/dashboard/archive-datasets/00112233445566778899aabbccddeeff/"
	for _, task := range []string{"backfill-tasks", "verify-tasks", "seal-tasks"} {
		for _, suffix := range []string{"", "/00112233445566778899aabbccddeeff/retry"} {
			for _, user := range []struct {
				name string
				want int
			}{{"archive", 410}, {"other", 403}, {"", 401}} {
				r := httptest.NewRequest("POST", base+task+suffix+"?site_id=site", strings.NewReader("{}"))
				r.Header.Set("X-Requested-With", "XMLHttpRequest")
				if user.name != "" {
					r.AddCookie(&http.Cookie{Name: "ct_session", Value: user.name})
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				if w.Code != user.want {
					t.Fatalf("%s %s: %d %s", task, user.name, w.Code, w.Body.String())
				}
				if w.Code == 410 && !strings.Contains(w.Body.String(), "legacy_archive_executor_retired") {
					t.Fatal(w.Body.String())
				}
			}
		}
		r := httptest.NewRequest("GET", base+task+"?site_id=site", nil)
		r.AddCookie(&http.Cookie{Name: "ct_session", Value: "archive"})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("history: %d %s", w.Code, w.Body.String())
		}
	}
	if s.reads != 3 {
		t.Fatal("legacy history did not reach stores")
	}
	r := httptest.NewRequest("GET", "/api/dashboard/log-archive-jobs?site_id=site", nil)
	r.AddCookie(&http.Cookie{Name: "ct_session", Value: "archive"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("new jobs: %d %s", w.Code, w.Body.String())
	}
	config := ac.Default()
	config.InstanceID = "inst"
	config.AgentID = "agent"
	config.Tasks = &aj.Settings{Collection: true, History: true}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []struct {
		name string
		want int
	}{{"archive", 200}, {"other", 403}} {
		r = httptest.NewRequest("PUT", "/api/dashboard/log-archive-jobs/site", bytes.NewReader(data))
		r.Header.Set("X-Requested-With", "XMLHttpRequest")
		r.AddCookie(&http.Cookie{Name: "ct_session", Value: user.name})
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != user.want {
			t.Fatalf("new write %s: %d %s", user.name, w.Code, w.Body.String())
		}
	}
	if s.writes != 1 {
		t.Fatal("new executor config must be writable only with archive permission")
	}
}
