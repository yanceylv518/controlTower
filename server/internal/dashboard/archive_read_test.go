package dashboard

import (
	"context"
	"controltower/server/internal/archivereader"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type jobReadFake struct {
	calls int
	err   error
	items []map[string]any
}

func (f *jobReadFake) ReadJob(_ context.Context, q archivereader.JobQuery) (archivereader.JobPage, error) {
	f.calls++
	return archivereader.JobPage{Items: f.items}, f.err
}

func TestArchiveOverviewOptionNames(t *testing.T) {
	options := map[string]map[string]bool{"user_id": {"12": true}, "channel_id": {"5": true}}
	f := &jobReadFake{items: []map[string]any{{"options": options}}}
	called := false
	h, cookie := foundationSession(t, ArchiveReadHandler{Reader: f, OptionNames: func(site string, ids map[string]map[string]bool) map[string]map[string]string {
		called = true
		if site != "site" || !ids["user_id"]["12"] || !ids["channel_id"]["5"] {
			t.Fatalf("wrong name scope: %s %v", site, ids)
		}
		return map[string]map[string]string{"user_id": {"12": "张三"}, "channel_id": {"5": "主渠道"}}
	}}, "admin", []string{"archive.manage"})
	r := httptest.NewRequest(http.MethodGet, "/api/dashboard/log-archive-read/overview?site_id=site&date=2026-09&dimension=model_name", nil)
	r.SetPathValue("kind", "overview")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var page struct {
		Items []struct {
			Options map[string]map[string]bool   `json:"options"`
			Names   map[string]map[string]string `json:"option_names"`
		} `json:"items"`
	}
	if w.Code != 200 || !called || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if page.Items[0].Names["user_id"]["12"] != "张三" || !page.Items[0].Options["user_id"]["12"] {
		t.Fatal(page)
	}
}

func TestArchiveReadPermissionAndErrors(t *testing.T) {
	for _, tc := range []struct {
		role        string
		permissions []string
		query       string
		err         error
		status      int
		calls       int
	}{
		{"viewer", []string{"archive.manage"}, "", nil, 403, 0},
		{"admin", []string{"billing.users"}, "", nil, 403, 0},
		{"admin", []string{"archive.manage"}, "&limit=10000", nil, 400, 0},
		{"admin", []string{"archive.manage"}, "", nil, 200, 1},
		{"admin", []string{"archive.manage"}, "", archivereader.ErrVersion, 409, 1},
		{"admin", []string{"archive.manage"}, "", errors.New("private-password-and-SQL"), 503, 1},
	} {
		for _, kind := range []string{"logs", "anomalies"} {
			f := &jobReadFake{err: tc.err}
			h, cookie := foundationSession(t, ArchiveReadHandler{Reader: f}, tc.role, tc.permissions)
			r := httptest.NewRequest(http.MethodGet, "/api/dashboard/log-archive-read/logs?site_id=site&date=2026-07-04"+tc.query, nil)
			r.SetPathValue("kind", kind)
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || f.calls != tc.calls || strings.Contains(w.Body.String(), "private-password") {
				t.Fatalf("%+v status=%d calls=%d body=%s", tc, w.Code, f.calls, w.Body.String())
			}
		}
	}
}
