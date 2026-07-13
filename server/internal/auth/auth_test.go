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

func TestMiddlewareRejectsMissingAndBlankCredentials(t *testing.T) {
	m, _ := setup(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	// No credentials at all.
	x := httptest.NewRecorder()
	RequireSessionOrToken(m, "legacy", next).ServeHTTP(x, httptest.NewRequest("GET", "/", nil))
	if x.Code != 401 {
		t.Fatalf("no credentials must be rejected, got %d", x.Code)
	}

	// A blank expected token must never match a credential-less request.
	x = httptest.NewRecorder()
	RequireSessionOrToken(m, "", next).ServeHTTP(x, httptest.NewRequest("GET", "/", nil))
	if x.Code != 401 {
		t.Fatalf("blank expected token must not grant access, got %d", x.Code)
	}

	// Session-authenticated mutation with the CSRF header passes.
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"password1"}`))
	w := httptest.NewRecorder()
	Handlers{M: m}.Login(w, r)
	c := w.Result().Cookies()[0]
	q := httptest.NewRequest("POST", "/", nil)
	q.AddCookie(c)
	q.Header.Set("X-Requested-With", "XMLHttpRequest")
	x = httptest.NewRecorder()
	RequireSessionOrToken(m, "legacy", next).ServeHTTP(x, q)
	if x.Code != 204 {
		t.Fatalf("csrf header must allow session mutation, got %d", x.Code)
	}
}

func TestHandlerFlowsMeLogoutPasswordAndLock(t *testing.T) {
	m, _ := setup(t)
	h := Handlers{M: m}
	login := func(password string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.Login(w, httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"`+password+`"}`)))
		return w
	}

	// me -> logout -> me 401
	c := login("password1").Result().Cookies()[0]
	withCookie := func(method, path, body string) *http.Request {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(c)
		return r
	}
	w := httptest.NewRecorder()
	h.Me(w, withCookie("GET", "/api/auth/me", ""))
	if w.Code != 200 {
		t.Fatalf("me: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.Logout(w, withCookie("POST", "/api/auth/logout", ""))
	w = httptest.NewRecorder()
	h.Me(w, withCookie("GET", "/api/auth/me", ""))
	if w.Code != 401 {
		t.Fatalf("me after logout: %d", w.Code)
	}

	// password change: wrong old rejected uniformly; success kills the session
	c = login("password1").Result().Cookies()[0]
	w = httptest.NewRecorder()
	h.Password(w, withCookie("POST", "/api/auth/password", `{"old_password":"nope","new_password":"password2"}`))
	if w.Code != 401 {
		t.Fatalf("wrong old password: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.Password(w, withCookie("POST", "/api/auth/password", `{"old_password":"password1","new_password":"password2"}`))
	if w.Code != 200 {
		t.Fatalf("password change: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.Me(w, withCookie("GET", "/api/auth/me", ""))
	if w.Code != 401 {
		t.Fatalf("old session must be invalid after password change: %d", w.Code)
	}
	if login("password2").Code != 200 {
		t.Fatal("new password must log in")
	}

	// lockout surfaces as 429 through the handler
	for i := 0; i < 5; i++ {
		login("bad")
	}
	if code := login("password2").Code; code != 429 {
		t.Fatalf("locked login must return 429, got %d", code)
	}
}
