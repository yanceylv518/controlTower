package auth

import (
	"controltower/server/internal/storage"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type Handlers struct{ M *Manager }

func write(w http.ResponseWriter, s int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s)
	_ = json.NewEncoder(w).Encode(v)
}
func (h Handlers) Login(w http.ResponseWriter, r *http.Request) {
	if h.M == nil {
		write(w, http.StatusServiceUnavailable, map[string]string{"error": "auth_unavailable"})
		return
	}
	var q struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&q) != nil {
		write(w, 401, map[string]string{"error": "invalid_credentials"})
		return
	}
	u, s, e := h.M.Login(q.Username, q.Password, time.Now().UTC())
	if e == ErrLocked {
		write(w, 429, map[string]string{"error": "locked"})
		return
	}
	if e != nil {
		write(w, 401, map[string]string{"error": "invalid_credentials"})
		return
	}
	// Secure is intentionally not set: TLS terminates at the reverse proxy.
	http.SetCookie(w, &http.Cookie{Name: "ct_session", Value: s.ID, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(h.M.TTL().Seconds())})
	write(w, 200, map[string]string{"username": u.Username, "role": u.Role})
}
func (h Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if h.M == nil {
		write(w, http.StatusServiceUnavailable, map[string]string{"error": "auth_unavailable"})
		return
	}
	if c, e := r.Cookie("ct_session"); e == nil {
		_ = h.M.Logout(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "ct_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	write(w, 200, map[string]bool{"ok": true})
}
func (h Handlers) current(r *http.Request) (storage.User, *http.Cookie, bool) {
	if h.M == nil {
		return storage.User{}, nil, false
	}
	c, e := r.Cookie("ct_session")
	if e != nil {
		return storage.User{}, nil, false
	}
	u, ok := h.M.Validate(c.Value, time.Now().UTC())
	return u, c, ok
}
func (h Handlers) Me(w http.ResponseWriter, r *http.Request) {
	u, _, ok := h.current(r)
	if !ok {
		write(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	write(w, 200, map[string]string{"username": u.Username, "role": u.Role})
}
func (h Handlers) Password(w http.ResponseWriter, r *http.Request) {
	u, c, ok := h.current(r)
	if !ok {
		write(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var q struct {
		Old string `json:"old_password"`
		New string `json:"new_password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&q)
	if h.M.ChangePassword(u.ID, q.Old, q.New, time.Now().UTC()) != nil {
		write(w, 401, map[string]string{"error": "invalid_credentials"})
		return
	}
	_ = h.M.Logout(c.Value)
	write(w, 200, map[string]bool{"ok": true})
}
func RequireSessionOrToken(m *Manager, token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, e := r.Cookie("ct_session"); e == nil {
			if _, ok := m.Validate(c.Value, time.Now().UTC()); ok {
				if r.Method != http.MethodGet && r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
					write(w, 403, map[string]string{"error": "csrf"})
					return
				}
				next.ServeHTTP(w, r)
				return
			}
		}
		v := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		// An empty expected token must never match: without this guard a
		// misconfigured blank CT_DASHBOARD_TOKEN would accept requests that
		// carry no credentials at all.
		if token != "" && v != "" && len(v) == len(token) && subtle.ConstantTimeCompare([]byte(v), []byte(token)) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		write(w, 401, map[string]string{"error": "unauthorized"})
	})
}
