package dashboard

import (
	"controltower/server/internal/storage"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type menuFake struct {
	raw    string
	fail   bool
	writes int
	audits []storage.OperationAudit
}

func (s *menuFake) MenuVisibility() (string, error) {
	if s.fail {
		return "", errors.New("offline")
	}
	return s.raw, nil
}
func (s *menuFake) SaveMenuVisibility(v, actor string, now time.Time) error {
	s.raw = v
	s.writes++
	return nil
}
func (s *menuFake) InsertOperationAudit(a storage.OperationAudit) error {
	s.audits = append(s.audits, a)
	return nil
}
func TestMenuVisibilityValidationAndPersistence(t *testing.T) {
	s := &menuFake{raw: "{}"}
	h := MenuVisibilityHandler{Store: s}
	call := func(method, body string) int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "/api/dashboard/menu-visibility", strings.NewReader(body)))
		return w.Code
	}
	for _, body := range []string{`{}`, `{"items":null}`, `{"items":{"/unknown":false}}`, `{"items":{"/":null}}`, `{"items":{"/":"false"}}`, `{"items":{}} {}`} {
		if got := call("PUT", body); got != 400 {
			t.Fatalf("%s: %d", body, got)
		}
	}
	if s.writes != 0 {
		t.Fatal("invalid input changed settings")
	}
	if got := call("PUT", `{"items":{"/":false,"/settings":false,"/customers":true}}`); got != 200 {
		t.Fatal(got)
	}
	if !strings.Contains(s.raw, `"/settings":false`) || len(s.audits) != 1 {
		t.Fatal("not persisted or audited")
	}
	if got := call("GET", ""); got != 200 {
		t.Fatal(got)
	}
	if got := call("POST", ""); got != 405 {
		t.Fatal(got)
	}
	s.fail = true
	if got := call("GET", ""); got != 500 {
		t.Fatal(got)
	}
}
