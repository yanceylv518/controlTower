package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	af "controltower/internal/archivecontract"
)

type verificationBoundaryReader struct {
	registration af.Registration
	date, run    string
	after        int64
	calls        int
	result       af.DayVerification
	err          error
}

func (r *verificationBoundaryReader) ReadDayVerification(_ context.Context, registration af.Registration, date, run string, after int64) (af.DayVerification, error) {
	r.calls++
	r.registration, r.date, r.run, r.after = registration, date, run, after
	return r.result, r.err
}

func verificationBoundaryRequest(h http.Handler, cookie *http.Cookie, method, query, dataset, date string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/dashboard/archive-datasets/"+dataset+"/days/"+date+"/verification"+query, nil)
	r.SetPathValue("dataset", dataset)
	r.SetPathValue("date", date)
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestArchiveVerificationReadRequiresManagementPermission(t *testing.T) {
	reg := foundationHandlerRegistration()
	store := &foundationHandlerStore{dataset: af.Dataset{Registration: reg}}
	reader := &verificationBoundaryReader{}
	h, cookie := foundationSession(t, ArchiveVerificationHandler{Store: store, Reader: reader}, "admin", []string{"billing.users"})
	w := verificationBoundaryRequest(h, cookie, http.MethodGet, "?site_id=site-a", reg.DatasetID, "2026-09-01")
	if w.Code != 403 || store.gets != 0 || reader.calls != 0 {
		t.Fatalf("billing permission accessed archive evidence: %d", w.Code)
	}
}

func TestArchiveVerificationReadValidatesCursorAndBinding(t *testing.T) {
	reg := foundationHandlerRegistration()
	run := strings.Repeat("3", 32)
	store := &foundationHandlerStore{dataset: af.Dataset{Registration: reg}}
	reader := &verificationBoundaryReader{}
	h, cookie := foundationSession(t, ArchiveVerificationHandler{Store: store, Reader: reader}, "admin", []string{"archive.manage"})
	for _, test := range []struct{ query, dataset, date string }{
		{"", reg.DatasetID, "2026-09-01"},
		{"?site_id=site-a", "bad", "2026-09-01"},
		{"?site_id=site-a", reg.DatasetID, "2026-02-30"},
		{"?site_id=site-a&run_id=bad", reg.DatasetID, "2026-09-01"},
		{"?site_id=site-a&after_id=-1", reg.DatasetID, "2026-09-01"},
		{"?site_id=site-a&after_id=1", reg.DatasetID, "2026-09-01"},
		{"?site_id=site-a&run_id=" + run + "&after_id=9223372036854775808", reg.DatasetID, "2026-09-01"},
		{"?site_id=site-a&run_id=" + run + "&after_id=9007199254740993.0", reg.DatasetID, "2026-09-01"},
	} {
		w := verificationBoundaryRequest(h, cookie, http.MethodGet, test.query, test.dataset, test.date)
		if w.Code != 400 || store.gets != 0 || reader.calls != 0 {
			t.Fatalf("invalid request reached data: %d %+v", w.Code, test)
		}
	}
	w := verificationBoundaryRequest(h, cookie, http.MethodPost, "?site_id=site-a", reg.DatasetID, "2026-09-01")
	if w.Code != 405 || store.gets != 0 || reader.calls != 0 {
		t.Fatalf("read endpoint accepted mutation: %d", w.Code)
	}
	w = verificationBoundaryRequest(h, cookie, http.MethodGet, "?site_id=foreign", reg.DatasetID, "2026-09-01")
	if w.Code != 404 || reader.calls != 0 {
		t.Fatalf("foreign site reached reader: %d", w.Code)
	}
	reader.result = af.DayVerification{Identity: reg.Identity, Date: "2026-09-01", SelectedRunID: run, ArchiveBilling: true, Issues: []af.VerificationIssue{{SourceID: 9007199254740993, Kind: "missing_target"}}}
	w = verificationBoundaryRequest(h, cookie, http.MethodGet, "?site_id=site-a&run_id="+run+"&after_id=9007199254740993&storage_ref=untrusted", reg.DatasetID, "2026-09-01")
	if w.Code != 200 || reader.registration != reg || reader.after != 9007199254740993 || reader.run != run || reader.date != "2026-09-01" || !strings.Contains(w.Body.String(), `"source_id":"9007199254740993"`) || !strings.Contains(w.Body.String(), `"archive_billing":false`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("valid read lost authority or precision: %d %s %+v", w.Code, w.Body.String(), reader)
	}
}

func TestArchiveVerificationReadFailsClosedOnUnavailableOrForeignEvidence(t *testing.T) {
	reg := foundationHandlerRegistration()
	run := strings.Repeat("3", 32)
	store := &foundationHandlerStore{dataset: af.Dataset{Registration: reg}}
	h, cookie := foundationSession(t, ArchiveVerificationHandler{Store: store}, "admin", []string{"archive.manage"})
	w := verificationBoundaryRequest(h, cookie, http.MethodGet, "?site_id=site-a", reg.DatasetID, "2026-09-01")
	if w.Code != 503 {
		t.Fatalf("missing reader fabricated evidence: %d", w.Code)
	}
	reader := &verificationBoundaryReader{err: errors.New("synthetic-reader-password")}
	h, cookie = foundationSession(t, ArchiveVerificationHandler{Store: store, Reader: reader}, "admin", []string{"archive.manage"})
	w = verificationBoundaryRequest(h, cookie, http.MethodGet, "?site_id=site-a", reg.DatasetID, "2026-09-01")
	if w.Code != 503 || strings.Contains(w.Body.String(), "synthetic-reader") {
		t.Fatalf("reader error leaked: %d %s", w.Code, w.Body.String())
	}
	reader.err = nil
	for _, mutate := range []func(*af.DayVerification){
		func(v *af.DayVerification) { v.SiteID = "foreign" },
		func(v *af.DayVerification) { v.SourceGenerationID = strings.Repeat("f", 32) },
		func(v *af.DayVerification) { v.Date = "2026-09-02" },
		func(v *af.DayVerification) { v.SelectedRunID = strings.Repeat("e", 32) },
	} {
		reader.result = af.DayVerification{Identity: reg.Identity, Date: "2026-09-01", SelectedRunID: run}
		mutate(&reader.result)
		w = verificationBoundaryRequest(h, cookie, http.MethodGet, "?site_id=site-a&run_id="+run, reg.DatasetID, "2026-09-01")
		if w.Code != 409 || strings.Contains(w.Body.String(), "selected_run_id") {
			t.Fatalf("foreign evidence escaped binding check: %d %s", w.Code, w.Body.String())
		}
	}
}
