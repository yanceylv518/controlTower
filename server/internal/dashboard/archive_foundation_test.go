package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	ac "controltower/internal/archivecontract"
	"controltower/server/internal/auth"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
)

type foundationHandlerStore struct {
	registration ac.Registration
	dataset      ac.Dataset
	applied      ac.CatalogSnapshot
	actor        string
	registers    int
	gets         int
	applies      int
	err          error
}

func (s *foundationHandlerStore) RegisterArchiveDataset(_ context.Context, r ac.Registration, actor string) error {
	s.registers++
	s.registration, s.actor = r, actor
	return s.err
}
func (s *foundationHandlerStore) GetArchiveDataset(_ context.Context, site, dataset string) (ac.Dataset, error) {
	s.gets++
	if site != s.dataset.SiteID || dataset != s.dataset.DatasetID {
		return ac.Dataset{}, ac.ErrNotFound
	}
	return s.dataset, s.err
}
func (s *foundationHandlerStore) ApplyArchiveCatalog(_ context.Context, snapshot ac.CatalogSnapshot) (bool, error) {
	s.applies++
	s.applied = snapshot
	return true, s.err
}

type foundationHandlerReader struct {
	registration ac.Registration
	snapshot     ac.CatalogSnapshot
	calls        int
	err          error
}

func (r *foundationHandlerReader) ReadCatalog(_ context.Context, registration ac.Registration) (ac.CatalogSnapshot, error) {
	r.calls++
	r.registration = registration
	return r.snapshot, r.err
}

func foundationHandlerRegistration() ac.Registration {
	return ac.Registration{
		Identity:   ac.Identity{SiteID: "site-a", DatasetID: strings.Repeat("1", 32), SourceGenerationID: strings.Repeat("2", 32)},
		StorageRef: "archive-reader-a", ArchiveFormatVersion: ac.FormatVersion,
		SchemaFingerprint: strings.Repeat("a", 64), SourceFingerprint: strings.Repeat("b", 64),
	}
}

func foundationSession(t *testing.T, h http.Handler, role string, permissions []string) (http.Handler, *http.Cookie) {
	t.Helper()
	users := ingest.NewMemoryStore()
	password, err := auth.HashPassword("synthetic-test-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = users.CreateUser(storage.User{Username: "archive-operator", Role: role, Permissions: permissions, Enabled: true, PasswordHash: password}); err != nil {
		t.Fatal(err)
	}
	manager := auth.NewManager(users, time.Hour)
	_, session, err := manager.Login("archive-operator", "synthetic-test-password", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return auth.RequireSessionOrToken(manager, "", h), &http.Cookie{Name: "ct_session", Value: session.ID}
}

func foundationRequest(h http.Handler, cookie *http.Cookie, method, path, dataset, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.SetPathValue("dataset", dataset)
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestArchiveFoundationPermissionPrecedesAnyStorage(t *testing.T) {
	for _, tc := range []struct {
		name        string
		role        string
		permissions []string
	}{
		{"anonymous", "", nil},
		{"viewer", "viewer", []string{"archive.manage"}},
		{"billing-only-admin", "admin", []string{"billing.users"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, reader := &foundationHandlerStore{}, &foundationHandlerReader{}
			var h http.Handler = ArchiveFoundationHandler{Store: store, Reader: reader}
			var cookie *http.Cookie
			if tc.role != "" {
				h, cookie = foundationSession(t, h, tc.role, tc.permissions)
			}
			w := foundationRequest(h, cookie, http.MethodPost, "/api/dashboard/archive-datasets", "", `{}`)
			if w.Code != http.StatusForbidden || store.registers != 0 || store.gets != 0 || reader.calls != 0 {
				t.Fatalf("permission must precede preflight/storage: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestArchiveFoundationRegistrationStrictBodyAndPreflight(t *testing.T) {
	registration := foundationHandlerRegistration()
	valid, err := json.Marshal(registration)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		body string
	}{
		{"malformed", `{`},
		{"null", `null`},
		{"trailing-json", string(valid) + `{}`},
		{"credential-field", strings.TrimSuffix(string(valid), "}") + `,"dsn":"must-not-be-accepted"}`},
		{"client-catalog", strings.TrimSuffix(string(valid), "}") + `,"days":[]}`},
		{"oversize", strings.TrimSuffix(string(valid), "}") + `,"padding":"` + strings.Repeat("x", 5000) + `"}`},
		{"unsupported-format", strings.Replace(string(valid), `"archive_format_version":2`, `"archive_format_version":99`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, reader := &foundationHandlerStore{}, &foundationHandlerReader{}
			h, cookie := foundationSession(t, ArchiveFoundationHandler{Store: store, Reader: reader}, "admin", []string{"archive.manage"})
			w := foundationRequest(h, cookie, http.MethodPost, "/api/dashboard/archive-datasets", "", tc.body)
			if w.Code != http.StatusBadRequest || store.registers != 0 || reader.calls != 0 {
				t.Fatalf("invalid registration reached preflight/storage: %d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"wrong-target-identity", ac.ErrIdentity, 409},
		{"newer-format", ac.ErrUnsupported, 409},
		{"connection-failure-redacted", errors.New("synthetic-secret-internal-connection-error"), 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, reader := &foundationHandlerStore{}, &foundationHandlerReader{err: tc.err}
			h, cookie := foundationSession(t, ArchiveFoundationHandler{Store: store, Reader: reader}, "admin", []string{"archive.manage"})
			w := foundationRequest(h, cookie, http.MethodPost, "/api/dashboard/archive-datasets", "", string(valid))
			if w.Code != tc.status || reader.calls != 1 || store.registers != 0 || strings.Contains(w.Body.String(), "synthetic-secret") {
				t.Fatalf("failed target preflight persisted or exposed internals: %d %s", w.Code, w.Body.String())
			}
		})
	}
	store, reader := &foundationHandlerStore{}, &foundationHandlerReader{snapshot: ac.CatalogSnapshot{Identity: registration.Identity}}
	h, cookie := foundationSession(t, ArchiveFoundationHandler{Store: store, Reader: reader}, "admin", []string{"archive.manage"})
	w := foundationRequest(h, cookie, http.MethodPost, "/api/dashboard/archive-datasets", "", string(valid))
	if w.Code != 200 || store.registers != 1 || store.actor != "archive-operator" || reader.registration != registration || store.registration != registration {
		t.Fatalf("permitted registration failed: %d %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"state":"paused"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"archive_billing":false`)) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("foundation registration must remain paused and unbillable: %s", w.Body.String())
	}
}

func TestArchiveFoundationSyncUsesRegisteredTargetOnly(t *testing.T) {
	registration := foundationHandlerRegistration()
	snapshot := ac.CatalogSnapshot{Identity: registration.Identity, CatalogRevision: 7, Days: []ac.CatalogDay{{Date: "2026-09-01", State: "unknown"}}}
	store := &foundationHandlerStore{dataset: ac.Dataset{Registration: registration, LifecycleState: "paused"}}
	reader := &foundationHandlerReader{snapshot: snapshot}
	h, cookie := foundationSession(t, ArchiveFoundationHandler{Store: store, Reader: reader}, "admin", []string{"archive.manage"})
	body := `{"site_id":"other-site","dataset_id":"attacker-dataset","storage_ref":"other-target","catalog_revision":"999","days":[{"date":"2026-09-01","state":"sealed"}]}`
	w := foundationRequest(h, cookie, http.MethodPost, "/api/dashboard/archive-datasets/"+registration.DatasetID+"/sync?site_id=site-a", registration.DatasetID, body)
	if w.Code != 200 || reader.registration != registration || reader.calls != 1 || store.applies != 1 || !reflect.DeepEqual(store.applied, snapshot) {
		t.Fatalf("sync trusted client body instead of registered server reader: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"catalog_revision":"7"`) || !strings.Contains(w.Body.String(), `"archive_billing":false`) {
		t.Fatalf("sync response misstates foundation capability: %s", w.Body.String())
	}
	reader.err = ac.ErrIdentity
	w = foundationRequest(h, cookie, http.MethodPost, "/api/dashboard/archive-datasets/"+registration.DatasetID+"/sync?site_id=site-a", registration.DatasetID, "")
	if w.Code != 409 || store.applies != 1 {
		t.Fatalf("identity mismatch must not update catalog: %d %s", w.Code, w.Body.String())
	}
	reader.err = nil
	w = foundationRequest(h, cookie, http.MethodGet, "/api/dashboard/archive-datasets/"+registration.DatasetID+"?site_id=other-site", registration.DatasetID, "")
	if w.Code != 404 || reader.calls != 2 || store.applies != 1 {
		t.Fatalf("wrong site lookup leaked target or triggered sync: %d %s", w.Code, w.Body.String())
	}
}

func TestArchiveFoundationUnavailableReaderCannotRegister(t *testing.T) {
	store := &foundationHandlerStore{}
	h, cookie := foundationSession(t, ArchiveFoundationHandler{Store: store}, "admin", []string{"archive.manage"})
	valid, _ := json.Marshal(foundationHandlerRegistration())
	w := foundationRequest(h, cookie, http.MethodPost, "/api/dashboard/archive-datasets", "", string(valid))
	if w.Code != 503 || store.registers != 0 {
		t.Fatalf("registration bypassed missing reader: %d %s", w.Code, w.Body.String())
	}
}
