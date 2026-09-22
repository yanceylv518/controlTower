package dashboard

import (
	"context"
	af "controltower/internal/archivecontract"
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
	activeDataset   string
	site            string
}

type workflowMonthStore struct {
	archiveMonthStore
	dailyMonth string
}

func (s *workflowMonthStore) ListArchiveWorkflowDays(_ context.Context, site, month string) ([]af.WorkflowDay, error) {
	s.dailyMonth = month
	return []af.WorkflowDay{{Date: month + "-01", State: "rebuilding", Counts: &af.WorkflowDayCounts{LogRows: "9007199254740993", RequestRows: "1", ErrorRows: "0"}}}, nil
}
func TestArchiveWorkflowDailyMonth(t *testing.T) {
	s := &workflowMonthStore{archiveMonthStore: archiveMonthStore{activeDataset: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	h, cookie := foundationSession(t, LogArchiveHandler{Store: s}, "admin", []string{"archive.manage"})
	w := foundationRequest(h, cookie, http.MethodGet, "/api/dashboard/log-archives?site_id=site&month=2026-06", "", "")
	var out struct {
		Items []ac.Item `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || s.dailyMonth != "2026-06" || len(out.Items) != 1 || len(out.Items[0].WorkflowDays) != 1 || out.Items[0].WorkflowDays[0].Counts.LogRows != "9007199254740993" {
		t.Fatalf("daily month failed: %d %s", w.Code, w.Body.String())
	}
}

func (s *archiveMonthStore) ListLogArchives(context.Context, string) ([]ac.Item, error) {
	site := s.site
	if site == "" {
		site = "site"
	}
	return []ac.Item{{SiteID: site, ActiveDatasetID: s.activeDataset, Status: ac.Status{
		Reconciliation: &ac.Reconciliation{Date: "2026-05-20", State: "matched", SourceRows: 42, TargetRows: 42},
	}}}, nil
}

func TestArchiveBoundSiteAdvertisesOnlyBackfillCapabilities(t *testing.T) {
	for _, tc := range []struct {
		dataset, site string
		want          bool
	}{
		{"", "site", false}, {"11111111111111111111111111111111", "site", true}, {"11111111111111111111111111111111", "other-site", false},
	} {
		s := &archiveMonthStore{activeDataset: tc.dataset, site: tc.site}
		h, cookie := foundationSession(t, LogArchiveHandler{Store: s}, "admin", []string{"archive.manage"})
		w := foundationRequest(h, cookie, http.MethodGet, "/api/dashboard/log-archives?site_id=site&month=2026-09", "", "")
		var out struct {
			Capabilities map[string]bool `json:"capabilities"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("capability response: %d %s", w.Code, w.Body.String())
		}
		if out.Capabilities["date_backfill"] != tc.want || out.Capabilities["coverage_catalog"] != tc.want || out.Capabilities["day_versions"] || out.Capabilities["archive_billing"] {
			t.Fatalf("capability escaped site/version boundary: %s", w.Body.String())
		}
	}
}
func (s *archiveMonthStore) LatestLogArchiveMonth(context.Context, string) (string, error) {
	s.lookups++
	return s.latest, nil
}
func (s *archiveMonthStore) ListLogArchiveDays(_ context.Context, _ string, month string) ([]ac.Day, error) {
	s.queried = month
	return []ac.Day{{Date: month + "-20", ArchivedRows: "42", RequestRows: "40", ErrorRows: "2", LastID: 9007199254740993}}, nil
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
			Month        string          `json:"month"`
			Items        []ac.Item       `json:"items"`
			Capabilities map[string]bool `json:"capabilities"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Month != tc.want || s.queried != tc.want || s.lookups != tc.lookups {
			t.Fatalf("case %+v: %d %s", tc, w.Code, w.Body.String())
		}
		if len(out.Items) != 1 || out.Items[0].SiteID != "site" || len(out.Items[0].Days) != 1 {
			t.Fatalf("legacy items missing: %s", w.Body.String())
		}
		day := out.Items[0].Days[0]
		if day.Date != tc.want+"-20" || day.ArchivedRows != "42" || day.RequestRows != "40" || day.ErrorRows != "2" || day.LastID != 9007199254740993 {
			t.Fatalf("legacy day changed: %+v", day)
		}
		if result := out.Items[0].Status.Reconciliation; result == nil || result.State != "matched" || result.SourceRows != 42 || result.TargetRows != 42 {
			t.Fatalf("legacy reconciliation changed: %s", w.Body.String())
		}
		for _, capability := range []string{"day_versions", "archive_billing", "date_backfill", "coverage_catalog"} {
			if enabled, present := out.Capabilities[capability]; !present || enabled {
				t.Fatalf("capability %q must be explicitly false even with a matched reconciliation: %s", capability, w.Body.String())
			}
		}
	}
}
