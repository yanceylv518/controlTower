package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
)

type backfillHandlerStore struct {
	calls                int
	actor, site, dataset string
	request              ac.BackfillRequest
	policy               af.CoveragePolicy
	err                  error
}

func (s *backfillHandlerStore) capture(site, dataset string) {
	s.calls++
	s.site, s.dataset = site, dataset
}
func (s *backfillHandlerStore) GetArchiveCoveragePolicy(_ context.Context, site, dataset string) (ac.ArchiveCoveragePolicy, error) {
	s.capture(site, dataset)
	return ac.ArchiveCoveragePolicy{CoveragePolicy: s.policy}, s.err
}
func (s *backfillHandlerStore) UpdateArchiveCoveragePolicy(_ context.Context, site, dataset string, p af.CoveragePolicy, actor string) (ac.ArchiveCoveragePolicy, error) {
	s.capture(site, dataset)
	s.actor, s.policy = actor, p
	return ac.ArchiveCoveragePolicy{CoveragePolicy: p}, s.err
}
func (s *backfillHandlerStore) GetArchiveCoverage(_ context.Context, site, dataset, month string) (ac.ArchiveCoverageMonth, error) {
	s.capture(site, dataset)
	return ac.ArchiveCoverageMonth{Month: month, Days: []ac.ArchiveCoverageDay{}}, s.err
}
func (s *backfillHandlerStore) CreateArchiveBackfill(_ context.Context, site, dataset string, r ac.BackfillRequest, actor string) (ac.BackfillTaskItem, error) {
	s.capture(site, dataset)
	s.actor, s.request = actor, r
	return ac.BackfillTaskItem{}, s.err
}
func (s *backfillHandlerStore) ListArchiveBackfills(_ context.Context, site, dataset string) ([]ac.BackfillTaskItem, error) {
	s.capture(site, dataset)
	return []ac.BackfillTaskItem{}, s.err
}
func (s *backfillHandlerStore) RetryArchiveBackfill(_ context.Context, site, dataset, task, actor string) (ac.BackfillTaskItem, error) {
	s.capture(site, dataset)
	s.actor = actor
	return ac.BackfillTaskItem{}, s.err
}

func TestArchiveBackfillHTTPPermissionAndBody(t *testing.T) {
	dataset := strings.Repeat("1", 32)
	for _, operation := range []string{"tasks", "coverage", "policy", "retry"} {
		for _, role := range []string{"viewer", "admin"} {
			store := &backfillHandlerStore{}
			h, cookie := foundationSession(t, ArchiveBackfillHandler{Store: store, Operation: operation}, role, []string{"billing.users"})
			w := foundationRequest(h, cookie, http.MethodGet, "/api/dashboard/archive-datasets/"+dataset+"/"+operation+"?site_id=site-a&month=2026-09", dataset, "")
			if w.Code != 403 || store.calls != 0 {
				t.Fatalf("%s/%s unauthorized access: %d", operation, role, w.Code)
			}
		}
	}
	for _, body := range []string{`{}`, `null`, `{"request_id":"` + strings.Repeat("2", 32) + `","date":"2026-09-01","from_unix":0}`, `{"request_id":"` + strings.Repeat("2", 32) + `","date":"2026-09-01"}{}`, `{"request_id":"invalid","date":"2026-09-01"}`, `{"request_id":"` + strings.Repeat("2", 32) + `","date":"2026-02-30"}`} {
		store := &backfillHandlerStore{}
		h, cookie := foundationSession(t, ArchiveBackfillHandler{Store: store, Operation: "tasks"}, "admin", []string{"archive.manage"})
		w := foundationRequest(h, cookie, http.MethodPost, "/api/dashboard/archive-datasets/"+dataset+"/backfill-tasks?site_id=site-a", dataset, body)
		if w.Code != 400 || store.calls != 0 {
			t.Fatalf("invalid body accepted: %d %s", w.Code, body)
		}
	}
	store := &backfillHandlerStore{}
	h, cookie := foundationSession(t, ArchiveBackfillHandler{Store: store, Operation: "tasks"}, "admin", []string{"archive.manage"})
	request := ac.BackfillRequest{RequestID: strings.Repeat("2", 32), Date: "2026-09-01"}
	body, _ := json.Marshal(request)
	w := foundationRequest(h, cookie, http.MethodPost, "/api/dashboard/archive-datasets/"+dataset+"/backfill-tasks?site_id=site-a", dataset, string(body))
	if w.Code != 200 || store.calls != 1 || store.actor != "archive-operator" || store.site != "site-a" || store.dataset != dataset || store.request != request {
		t.Fatalf("valid request lost binding/actor: %d %+v", w.Code, store)
	}
	store.err = errors.New("synthetic-database-credential")
	w = foundationRequest(h, cookie, http.MethodGet, "/api/dashboard/archive-datasets/"+dataset+"/backfill-tasks?site_id=site-a", dataset, "")
	if w.Code != 503 || strings.Contains(w.Body.String(), "synthetic-database") {
		t.Fatalf("storage error exposed: %d %s", w.Code, w.Body.String())
	}
}

func TestArchiveBackfillHTTPPolicyAndNonbillingCoverage(t *testing.T) {
	dataset := strings.Repeat("1", 32)
	p := af.DefaultCoveragePolicy()
	p.CoverageFrom = "2026-09-01"
	p.SourceRetainedFrom = "2026-09-01"
	p.Evidence = "confirmed retention policy"
	p.Revision = 9007199254740993
	body, _ := json.Marshal(p)
	store := &backfillHandlerStore{}
	h, cookie := foundationSession(t, ArchiveBackfillHandler{Store: store, Operation: "policy"}, "admin", []string{"archive.manage"})
	w := foundationRequest(h, cookie, http.MethodPut, "/api/dashboard/archive-datasets/"+dataset+"/coverage-policy?site_id=site-a", dataset, string(body))
	if w.Code != 200 || store.calls != 1 || store.policy.Revision != p.Revision || store.actor != "archive-operator" {
		t.Fatalf("policy roundtrip: %d %s", w.Code, w.Body.String())
	}
	store.calls = 0
	w = foundationRequest(h, cookie, http.MethodPut, "/api/dashboard/archive-datasets/"+dataset+"/coverage-policy?site_id=site-a", dataset, strings.TrimSuffix(string(body), "}")+`,"confirmed_by":"attacker"}`)
	if w.Code != 400 || store.calls != 0 {
		t.Fatal("policy actor accepted from request")
	}
	h, cookie = foundationSession(t, ArchiveBackfillHandler{Store: store, Operation: "coverage"}, "admin", []string{"archive.manage"})
	w = foundationRequest(h, cookie, http.MethodGet, "/api/dashboard/archive-datasets/"+dataset+"/coverage?site_id=site-a&month=2026-09", dataset, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"archive_billing":false`) || !strings.Contains(w.Body.String(), `"day_versions":false`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("coverage advertised billing: %d %s", w.Code, w.Body.String())
	}
}
