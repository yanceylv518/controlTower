package dashboard

import (
	"context"
	ar "controltower/server/internal/archivereader"
	"controltower/server/internal/secrets"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type connectionMemory struct {
	c     ar.Connection
	saved int
}

func (s *connectionMemory) ArchiveSiteExists(_ context.Context, site string) (bool, error) {
	return site == "site", nil
}
func (s *connectionMemory) LoadArchiveConnection(context.Context, string) (ar.Connection, error) {
	if s.c.Version == 0 {
		return s.c, ar.ErrConnectionMissing
	}
	return s.c, nil
}
func (s *connectionMemory) SaveArchiveConnection(_ context.Context, _ string, c ar.Connection, _ string) error {
	if c.Version != s.c.Version {
		return ar.ErrConnectionConflict
	}
	c.Version++
	s.c = c
	s.saved++
	return nil
}

func TestArchiveConnectionTestSaveSecretsAndBinding(t *testing.T) {
	store := &connectionMemory{}
	hash := strings.Repeat("a", 64)
	probes := 0
	handler := ArchiveConnectionHandler{Store: store, SecretKey: "test-key", Probe: func(_ context.Context, c ar.Connection) (string, error) {
		probes++
		pass, err := secrets.Decrypt("test-key", c.EncryptedPassword)
		if err != nil || pass != "synthetic-password" {
			t.Fatal("password not encrypted")
		}
		if c.SourceHash != "" && c.SourceHash != hash {
			return "", ar.ErrIdentity
		}
		return hash, nil
	}}
	h, cookie := foundationSession(t, handler, "admin", []string{"archive.manage"})
	request := func(method, path string, body map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, strings.NewReader(string(b)))
		r.AddCookie(cookie)
		r.Header.Set("X-Requested-With", "XMLHttpRequest")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	input := map[string]any{"host": "localhost", "port": 3306, "database": "archive", "username": "reader", "password": "synthetic-password", "tls": true, "version": 0}
	path := "/api/dashboard/log-archive-connection?site_id=site"
	if w := request("PUT", path, input); w.Code != 400 || probes != 0 {
		t.Fatalf("untested save %d", w.Code)
	}
	w := request("POST", "/api/dashboard/log-archive-connection/test?site_id=site", input)
	if w.Code != 200 || store.saved != 0 || strings.Contains(w.Body.String(), "synthetic-password") || strings.Contains(w.Body.String(), "v1:") {
		t.Fatal(w.Body.String())
	}
	input["source_hash"] = hash
	if w = request("PUT", path, input); w.Code != 200 || store.saved != 1 || probes != 2 {
		t.Fatalf("save %d %s", w.Code, w.Body.String())
	}
	input["password"] = ""
	input["version"] = 1
	if w = request("PUT", path, input); w.Code != 200 || store.saved != 2 {
		t.Fatalf("preserve password %d %s", w.Code, w.Body.String())
	}
	if w = request("PUT", path, input); w.Code != 409 || store.saved != 2 {
		t.Fatal("stale save accepted")
	}
	if w = request("GET", path, nil); w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-password") || strings.Contains(w.Body.String(), "v1:") {
		t.Fatal("credential leak", w.Body.String())
	}
	input["version"] = 2
	hash = strings.Repeat("b", 64)
	input["source_hash"] = hash
	if w = request("PUT", path, input); w.Code != 422 || store.saved != 2 {
		t.Fatal("identity changed", w.Body.String())
	}
	if w = request("POST", "/api/dashboard/log-archive-connection/test?site_id=other", input); w.Code != 404 {
		t.Fatal("unknown site accepted")
	}
}

func TestArchiveConnectionRejectsUnauthorizedAndRedactsErrors(t *testing.T) {
	for _, role := range []string{"viewer", "admin"} {
		store := &connectionMemory{}
		handler := ArchiveConnectionHandler{Store: store, SecretKey: "key", Probe: func(context.Context, ar.Connection) (string, error) { return "", errors.New("private-password") }}
		perms := []string{"billing.users"}
		if role == "viewer" {
			perms = []string{"archive.manage"}
		}
		h, cookie := foundationSession(t, handler, role, perms)
		w := foundationRequest(h, cookie, http.MethodGet, "/api/dashboard/log-archive-connection?site_id=site", "", "")
		if w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
}
