package auth

import (
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func setup(t *testing.T) (*Manager, *ingest.MemoryStore) {
	s := ingest.NewMemoryStore()
	h, e := HashPassword("password1")
	if e != nil {
		t.Fatal(e)
	}
	n := time.Now().UTC()
	_ = s.CreateUser(storage.User{Username: "admin", PasswordHash: h, Role: "admin", CreatedAt: n, UpdatedAt: n})
	return NewManager(s, time.Hour), s
}
func TestPasswordHash(t *testing.T) {
	h, e := HashPassword("secret123")
	if e != nil || !VerifyPassword(h, "secret123") || VerifyPassword(h, "wrong") || VerifyPassword("bad", "x") {
		t.Fatal("password hash roundtrip failed")
	}
}
func TestLoginLockAndSession(t *testing.T) {
	m, _ := setup(t)
	n := time.Now()
	for i := 0; i < 5; i++ {
		_, _, _ = m.Login("admin", "bad", n)
	}
	if _, _, e := m.Login("admin", "password1", n); e != ErrLocked {
		t.Fatal(e)
	}
	_, s, e := m.Login("admin", "password1", n.Add(11*time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := m.Validate(s.ID, n.Add(12*time.Minute)); !ok {
		t.Fatal("invalid session")
	}
	_ = m.Logout(s.ID)
	if _, ok := m.Validate(s.ID, n); ok {
		t.Fatal("logout failed")
	}
}
func TestHandlersAndMiddleware(t *testing.T) {
	m, _ := setup(t)
	h := Handlers{M: m}
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"password1"}`))
	w := httptest.NewRecorder()
	h.Login(w, r)
	if w.Code != 200 || len(w.Result().Cookies()) == 0 {
		t.Fatal(w.Code)
	}
	c := w.Result().Cookies()[0]
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mw := RequireSessionOrToken(m, "legacy", next)
	q := httptest.NewRequest("GET", "/", nil)
	q.AddCookie(c)
	x := httptest.NewRecorder()
	mw.ServeHTTP(x, q)
	if x.Code != 204 {
		t.Fatal(x.Code)
	}
	q = httptest.NewRequest("POST", "/", nil)
	q.AddCookie(c)
	x = httptest.NewRecorder()
	mw.ServeHTTP(x, q)
	if x.Code != 403 {
		t.Fatal(x.Code)
	}
	q = httptest.NewRequest("POST", "/", nil)
	q.Header.Set("Authorization", "Bearer legacy")
	x = httptest.NewRecorder()
	mw.ServeHTTP(x, q)
	if x.Code != 204 {
		t.Fatal(x.Code)
	}
}
