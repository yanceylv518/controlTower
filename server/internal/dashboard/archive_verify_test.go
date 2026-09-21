package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
)

type archiveVerifyHandlerStore struct {
	calls                      int
	site, dataset, actor, task string
	verify                     ac.ReconcileRequest
	seal                       ac.SealRequest
}

func (s *archiveVerifyHandlerStore) capture(site, dataset, actor string) {
	s.calls++
	s.site, s.dataset, s.actor = site, dataset, actor
}
func (s *archiveVerifyHandlerStore) CreateArchiveReconcile(_ context.Context, site, dataset string, request ac.ReconcileRequest, actor string) (ac.ReconcileTaskItem, error) {
	s.capture(site, dataset, actor)
	s.verify = request
	return ac.ReconcileTaskItem{}, nil
}
func (s *archiveVerifyHandlerStore) ListArchiveReconciles(_ context.Context, site, dataset string) ([]ac.ReconcileTaskItem, error) {
	s.capture(site, dataset, "")
	return []ac.ReconcileTaskItem{}, nil
}
func (s *archiveVerifyHandlerStore) RetryArchiveReconcile(_ context.Context, site, dataset, task, actor string) (ac.ReconcileTaskItem, error) {
	s.capture(site, dataset, actor)
	s.task = task
	return ac.ReconcileTaskItem{}, nil
}
func (s *archiveVerifyHandlerStore) CreateArchiveSeal(_ context.Context, site, dataset string, request ac.SealRequest, actor string) (ac.SealTaskItem, error) {
	s.capture(site, dataset, actor)
	s.seal = request
	return ac.SealTaskItem{}, nil
}
func (s *archiveVerifyHandlerStore) ListArchiveSeals(_ context.Context, site, dataset string) ([]ac.SealTaskItem, error) {
	s.capture(site, dataset, "")
	return []ac.SealTaskItem{}, nil
}
func (s *archiveVerifyHandlerStore) RetryArchiveSeal(_ context.Context, site, dataset, task, actor string) (ac.SealTaskItem, error) {
	s.capture(site, dataset, actor)
	s.task = task
	return ac.SealTaskItem{}, nil
}

func TestArchiveVerificationHTTPRequiresPermissionAndCSRF(t *testing.T) {
	dataset := strings.Repeat("1", 32)
	for _, operation := range []string{"verify", "seal", "verify_retry", "seal_retry"} {
		store := &archiveVerifyHandlerStore{}
		h, cookie := foundationSession(t, ArchiveVerifyHandler{Store: store, Operation: operation}, "admin", []string{"billing.users"})
		w := foundationRequest(h, cookie, http.MethodGet, "/api/dashboard/archive-datasets/"+dataset+"/verify-tasks?site_id=site-a", dataset, "")
		if w.Code != 403 || store.calls != 0 {
			t.Fatalf("billing permission granted archive management: %s %d", operation, w.Code)
		}
	}
	store := &archiveVerifyHandlerStore{}
	h, cookie := foundationSession(t, ArchiveVerifyHandler{Store: store, Operation: "seal"}, "admin", []string{"archive.manage"})
	r := httptest.NewRequest(http.MethodPost, "/api/dashboard/archive-datasets/"+dataset+"/seal-tasks?site_id=site-a", strings.NewReader(`{"request_id":"`+strings.Repeat("2", 32)+`","dates":["2026-09-01"]}`))
	r.SetPathValue("dataset", dataset)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || store.calls != 0 {
		t.Fatalf("mutation without CSRF accepted: %d", w.Code)
	}
}

func TestArchiveVerificationHTTPRejectsBareMatchAndForce(t *testing.T) {
	dataset, requestID := strings.Repeat("1", 32), strings.Repeat("2", 32)
	request := ac.ReconcileRequest{RequestID: requestID, Date: "2026-09-01", Assurance: af.VerificationAssurance{StableBeforeUnix: 1788364800, ValidUntilUnix: 1788451200, Evidence: "explicit operator assurance"}}
	encoded, _ := json.Marshal(request)
	store := &archiveVerifyHandlerStore{}
	h, cookie := foundationSession(t, ArchiveVerifyHandler{Store: store, Operation: "verify"}, "admin", []string{"archive.manage"})
	for _, body := range []string{`{}`, `null`, `{"request_id":"` + requestID + `","date":"2026-09-01","matched":true}`, strings.TrimSuffix(string(encoded), "}") + `,"force":true}`, string(encoded) + `{}`} {
		w := foundationRequest(h, cookie, http.MethodPost, "/api/dashboard/archive-datasets/"+dataset+"/verify-tasks?site_id=site-a", dataset, body)
		if w.Code != 400 || store.calls != 0 {
			t.Fatalf("untrusted verification accepted: %d %s", w.Code, body)
		}
	}
	w := foundationRequest(h, cookie, http.MethodPost, "/api/dashboard/archive-datasets/"+dataset+"/verify-tasks?site_id=site-a", dataset, string(encoded))
	if w.Code != 200 || store.calls != 1 || store.actor != "archive-operator" || store.site != "site-a" || store.dataset != dataset || store.verify != request {
		t.Fatalf("request lost actor/binding: %d %+v", w.Code, store)
	}
	for _, operation := range []string{"verify", "seal"} {
		h, cookie = foundationSession(t, ArchiveVerifyHandler{Store: store, Operation: operation}, "admin", []string{"archive.manage"})
		w = foundationRequest(h, cookie, http.MethodGet, "/api/dashboard/archive-datasets/"+dataset+"/verify-tasks?site_id=site-a", dataset, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"archive_billing":false`) || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("operational list became billing authorization: %s %d %s", operation, w.Code, w.Body.String())
		}
	}
}

func TestArchiveSealHTTPExplicitCohortAndRetry(t *testing.T) {
	dataset, task := strings.Repeat("1", 32), strings.Repeat("3", 32)
	store := &archiveVerifyHandlerStore{}
	h, cookie := foundationSession(t, ArchiveVerifyHandler{Store: store, Operation: "seal"}, "admin", []string{"archive.manage"})
	for _, body := range []string{`{"request_id":"` + task + `","dates":[]}`, `{"request_id":"` + task + `","dates":["2026-09-01","2026-09-01"]}`, `{"request_id":"` + task + `","dates":["2026-09-01"],"force":true}`} {
		w := foundationRequest(h, cookie, http.MethodPost, "/api/dashboard/archive-datasets/"+dataset+"/seal-tasks?site_id=site-a", dataset, body)
		if w.Code != 400 || store.calls != 0 {
			t.Fatalf("invalid cohort accepted: %d %s", w.Code, body)
		}
	}
	w := foundationRequest(h, cookie, http.MethodPost, "/api/dashboard/archive-datasets/"+dataset+"/seal-tasks?site_id=site-a", dataset, `{"request_id":"`+task+`","dates":["2026-09-02","2026-09-01"]}`)
	if w.Code != 200 || !reflect.DeepEqual(store.seal.Dates, []string{"2026-09-01", "2026-09-02"}) {
		t.Fatalf("cohort not canonical: %d %+v", w.Code, store.seal)
	}
	h, cookie = foundationSession(t, ArchiveVerifyHandler{Store: store, Operation: "seal_retry"}, "admin", []string{"archive.manage"})
	for _, body := range []string{`{"force":true}`, `{}`} {
		r := httptest.NewRequest(http.MethodPost, "/api/dashboard/archive-datasets/"+dataset+"/seal-tasks/"+task+"/retry?site_id=site-a", strings.NewReader(body))
		r.SetPathValue("dataset", dataset)
		r.SetPathValue("task", task)
		r.AddCookie(cookie)
		r.Header.Set("X-Requested-With", "XMLHttpRequest")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if body != `{}` && w.Code != 400 {
			t.Fatalf("force retry accepted: %d", w.Code)
		}
		if body == `{}` && (w.Code != 200 || store.task != task || store.actor != "archive-operator") {
			t.Fatalf("retry binding lost: %d %+v", w.Code, store)
		}
	}
}
