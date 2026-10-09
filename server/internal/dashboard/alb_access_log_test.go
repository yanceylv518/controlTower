package dashboard

import (
	"context"
	"controltower/server/internal/alblog"
	"controltower/server/internal/ingest"
	"controltower/server/internal/secrets"
	"encoding/json"

	"net/http/httptest"
	"strings"
	"testing"
)

func TestALBConfigSaveTestAndCredentialPreservation(t *testing.T) {
	store := ingest.NewMemoryStore()
	calls := 0
	fail := false
	handler := &ALBAccessLogHandler{Store: store, SecretKey: "test-key", Probe: func(_ context.Context, c alblog.Config) alblog.Result {
		calls++
		secret, err := secrets.Decrypt("test-key", c.SecretCipher)
		if err != nil || secret != "synthetic-secret" {
			t.Fatal("not encrypted")
		}
		if fail {
			return alblog.Result{Status: "failed", Code: "alb_auth_failed", TestedAt: "2026-10-09T00:00:00Z"}
		}
		return alblog.Result{Status: "no_data", TestedAt: "2026-10-09T00:00:00Z"}
	}}
	h, cookie := foundationSession(t, handler, "admin", []string{"settings.manage"})
	request := func(method, path string, input any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(input)
		r := httptest.NewRequest(method, path, strings.NewReader(string(body)))
		r.AddCookie(cookie)
		r.Header.Set("X-Requested-With", "XMLHttpRequest")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if strings.Contains(w.Body.String(), "synthetic-secret") || strings.Contains(w.Body.String(), "v1:") {
			t.Fatal("secret leaked")
		}
		return w
	}
	path := "/api/dashboard/alb-access-log"
	input := map[string]any{"endpoint": "https://cn-hangzhou.log.aliyuncs.com", "project": "test-project", "logstore": "alb-access-log", "alb_id": "alb-test123", "access_key_id": "testAccessKey123", "access_key_secret": "synthetic-secret", "version": 0}
	if w := request("POST", path+"/test", input); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := store.LoadALBLogConfig(context.Background()); err != alblog.ErrMissing {
		t.Fatal("draft test saved")
	}
	if w := request("PUT", path, input); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, _ := store.LoadALBLogConfig(context.Background())
	if saved.Version != 1 || saved.LastTest.Status != "no_data" {
		t.Fatal(saved)
	}
	cipher := saved.SecretCipher
	input["access_key_secret"] = ""
	input["version"] = 1
	if w := request("POST", path+"/test", input); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, _ = store.LoadALBLogConfig(context.Background())
	if saved.Version != 2 || saved.SecretCipher != cipher {
		t.Fatal("saved test or blank retention failed")
	}
	if w := request("PUT", path, input); w.Code != 409 || calls != 3 {
		t.Fatal("stale update reached cloud")
	}
	input["version"] = 2
	input["alb_id"] = "alb-draft"
	if w := request("POST", path+"/test", input); w.Code != 200 {
		t.Fatal(w.Code)
	}
	saved, _ = store.LoadALBLogConfig(context.Background())
	if saved.Version != 2 || saved.ALBID != "alb-test123" {
		t.Fatal("draft overwrote saved")
	}
	input["access_key_id"] = "rotatedAccessKey"
	if w := request("PUT", path, input); w.Code != 400 {
		t.Fatal("rotated ID reused unrelated secret")
	}
	input["access_key_id"] = "testAccessKey123"
	fail = true
	if w := request("PUT", path, input); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, _ = store.LoadALBLogConfig(context.Background())
	if saved.LastTest.Status != "failed" || saved.LastTest.Code != "alb_auth_failed" {
		t.Fatal("failed status not persisted")
	}
	if w := request("GET", path, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "alb_auth_failed") {
		t.Fatal("status missing")
	}
}
func TestALBConfigPermissionsAndInvalidInput(t *testing.T) {
	handler := &ALBAccessLogHandler{Store: ingest.NewMemoryStore(), SecretKey: "key", Probe: func(context.Context, alblog.Config) alblog.Result {
		t.Fatal("unexpected cloud request")
		return alblog.Result{}
	}}
	for _, role := range []string{"viewer", "admin"} {
		h, cookie := foundationSession(t, handler, role, []string{"monitor.runtime"})
		for _, method := range []string{"GET", "PUT", "POST"} {
			r := httptest.NewRequest(method, "/api/dashboard/alb-access-log", strings.NewReader("{}"))
			r.AddCookie(cookie)
			r.Header.Set("X-Requested-With", "XMLHttpRequest")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatalf("%s %s %d", role, method, w.Code)
			}
		}
	}
	h, cookie := foundationSession(t, handler, "admin", []string{"settings.manage"})
	for _, body := range []string{`{"unexpected":true}`, `{} {}`, strings.Repeat("x", 9000)} {
		r := httptest.NewRequest("PUT", "/api/dashboard/alb-access-log", strings.NewReader(body))
		r.AddCookie(cookie)
		r.Header.Set("X-Requested-With", "XMLHttpRequest")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}
