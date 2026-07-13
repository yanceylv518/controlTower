package auth

import (
	"context"
	"controltower/server/internal/storage"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type actorKey struct{}

func Actor(r *http.Request) string { v, _ := r.Context().Value(actorKey{}).(string); return v }
func withActor(r *http.Request, v string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), actorKey{}, v))
}

type Handlers struct {
	M       *Manager
	Limiter *IPLimiter
}

type IPLimiter struct {
	mu      sync.Mutex
	entries map[string][]time.Time
	now     func() time.Time
}

func NewIPLimiter() *IPLimiter { return &IPLimiter{entries: map[string][]time.Time{}, now: time.Now} }
func (l *IPLimiter) Allow(ip string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now().UTC()
	cutoff := now.Add(-time.Minute)
	items := l.entries[ip][:0]
	for _, t := range l.entries[ip] {
		if t.After(cutoff) {
			items = append(items, t)
		}
	}
	if len(items) >= 10 {
		l.entries[ip] = items
		return false
	}
	l.entries[ip] = append(items, now)
	if len(l.entries) > 1000 {
		for key, v := range l.entries {
			if len(v) == 0 || !v[len(v)-1].After(cutoff) {
				delete(l.entries, key)
			}
		}
	}
	return true
}
func clientIP(r *http.Request) string {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e == nil {
		return host
	}
	return r.RemoteAddr
} // Deliberately ignore X-Forwarded-For; the reverse proxy must enforce its own IP limits.
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
	if h.Limiter != nil && !h.Limiter.Allow(clientIP(r)) {
		write(w, 429, map[string]string{"error": "rate_limited"})
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
			if u, ok := m.Validate(c.Value, time.Now().UTC()); ok {
				if r.Method != http.MethodGet && r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
					write(w, 403, map[string]string{"error": "csrf"})
					return
				}
				next.ServeHTTP(w, withActor(r, u.Username))
				return
			}
		}
		v := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		// An empty expected token must never match: without this guard a
		// misconfigured blank CT_DASHBOARD_TOKEN would accept requests that
		// carry no credentials at all.
		if token != "" && v != "" && len(v) == len(token) && subtle.ConstantTimeCompare([]byte(v), []byte(token)) == 1 {
			next.ServeHTTP(w, withActor(r, "token"))
			return
		}
		write(w, 401, map[string]string{"error": "unauthorized"})
	})
}
