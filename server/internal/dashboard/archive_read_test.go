package dashboard

import (
	"context"
	"controltower/server/internal/archivereader"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type jobReadFake struct {
	calls int
	err   error
}

func (f *jobReadFake) ReadJob(_ context.Context, q archivereader.JobQuery) (archivereader.JobPage, error) {
	f.calls++
	return archivereader.JobPage{Items: []map[string]any{}}, f.err
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
