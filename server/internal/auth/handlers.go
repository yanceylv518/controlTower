package auth

import (
	"context"
	"controltower/server/internal/auditmeta"
	"controltower/server/internal/storage"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type actorKey struct{}
type userKey struct{}

const (
	authRequestBodyLimit    = 8 << 10
	accountRequestBodyLimit = 64 << 10
	maxLoginIPEntries       = 8192
	maxLoginAttemptsPerIP   = 10
	loginIPAttemptWindow    = time.Minute
	loginIPCleanupInterval  = time.Minute
)

func Actor(r *http.Request) string { v, _ := r.Context().Value(actorKey{}).(string); return v }
func withActor(r *http.Request, v string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), actorKey{}, v))
}
func CurrentUser(r *http.Request) (storage.User, bool) {
	v, ok := r.Context().Value(userKey{}).(storage.User)
	return v, ok
}
func withUser(r *http.Request, u storage.User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userKey{}, u))
}

type Handlers struct {
	M       *Manager
	Limiter *IPLimiter
	Audit   interface {
		InsertOperationAudit(storage.OperationAudit) error
	}
}

type IPLimiter struct {
	mu          sync.Mutex
	entries     map[string][]time.Time
	now         func() time.Time
	nextCleanup time.Time
}

func NewIPLimiter() *IPLimiter { return &IPLimiter{entries: map[string][]time.Time{}, now: time.Now} }
func (l *IPLimiter) Allow(ip string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now().UTC()
	cutoff := now.Add(-loginIPAttemptWindow)
	if l.nextCleanup.IsZero() || !now.Before(l.nextCleanup) {
		l.pruneExpired(cutoff)
		l.nextCleanup = now.Add(loginIPCleanupInterval)
	}
	items, exists := l.entries[ip]
	items = pruneLoginIPAttempts(items, cutoff)
	if exists {
		if len(items) == 0 {
			delete(l.entries, ip)
		} else {
			l.entries[ip] = items
		}
	}
	if len(items) >= maxLoginAttemptsPerIP {
		return false
	}
	if !exists && len(l.entries) >= maxLoginIPEntries {
		// 表满时拒绝新来源，避免请求来源不断变化导致限流状态无上限增长。
		return false
	}
	l.entries[ip] = append(items, now)
	return true
}

func (l *IPLimiter) pruneExpired(cutoff time.Time) {
	for ip, attempts := range l.entries {
		attempts = pruneLoginIPAttempts(attempts, cutoff)
		if len(attempts) == 0 {
			delete(l.entries, ip)
		} else {
			l.entries[ip] = attempts
		}
	}
}

func pruneLoginIPAttempts(attempts []time.Time, cutoff time.Time) []time.Time {
	kept := attempts[:0]
	for _, timestamp := range attempts {
		if timestamp.After(cutoff) {
			kept = append(kept, timestamp)
		}
	}
	return kept
}

func decodeAuthBody(w http.ResponseWriter, r *http.Request, value any, limit int64) error {
	if r.Body == nil {
		r.Body = http.NoBody
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeAuthDecodeError(w http.ResponseWriter, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		write(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request_too_large"})
		return
	}
	write(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
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
	auditmeta.SetActor(r, "unknown", "human", "", "session")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		write(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
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
	if err := decodeAuthBody(w, r, &q, authRequestBodyLimit); err != nil {
		writeAuthDecodeError(w, err)
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
	auditmeta.SetActor(r, u.Username, "human", u.Role, "session")
	if err := h.sessionAudit(r, u, "auth.login"); err != nil {
		_ = h.M.Logout(s.ID)
		write(w, http.StatusServiceUnavailable, map[string]string{"error": "audit_failed"})
		return
	}
	// Secure is intentionally not set: TLS terminates at the reverse proxy.
	http.SetCookie(w, &http.Cookie{Name: "ct_session", Value: s.ID, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(h.M.TTL().Seconds())})
	write(w, 200, userResponse(u))
}
func (h Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	auditmeta.SetActor(r, "unknown", "human", "", "session")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		write(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	if h.M == nil {
		write(w, http.StatusServiceUnavailable, map[string]string{"error": "auth_unavailable"})
		return
	}
	u, c, ok := h.current(r)
	if c != nil {
		if err := h.M.Logout(c.Value); err != nil {
			write(w, http.StatusInternalServerError, map[string]string{"error": "logout_failed"})
			return
		}
	}
	if ok {
		if err := h.sessionAudit(r, u, "auth.logout"); err != nil {
			write(w, http.StatusServiceUnavailable, map[string]string{"error": "audit_failed"})
			return
		}
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
	if ok {
		auditmeta.SetActor(r, u.Username, "human", u.Role, "session")
	}
	return u, c, ok
}
func (h Handlers) Me(w http.ResponseWriter, r *http.Request) {
	u, _, ok := h.current(r)
	if !ok {
		write(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	write(w, 200, userResponse(u))
}

type userResponseDTO struct {
	ID           int64    `json:"id,omitempty"`
	Username     string   `json:"username"`
	Role         string   `json:"role"`
	ScopeSite    string   `json:"scope_site"`
	ScopeUserIDs []int64  `json:"scope_user_ids"`
	Enabled      bool     `json:"enabled"`
	DisplayName  string   `json:"display_name"`
	Permissions  []string `json:"permissions"`
}

func userResponse(u storage.User) userResponseDTO {
	permissions := []string{}
	if u.Role == "admin" {
		permissions = ExpandPermissions(u.Permissions)
		if storage.IsFullAdmin(u) {
			permissions = []string{"*"}
		}
	}
	return userResponseDTO{u.ID, u.Username, u.Role, u.ScopeSite, u.ScopeUserIDs, u.Enabled, u.DisplayName, permissions}
}

func (h Handlers) Users(w http.ResponseWriter, r *http.Request) {
	u, _, ok := h.current(r)
	if !ok {
		write(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	if !HasPermission(u, "accounts.manage") {
		write(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	if r.Method == http.MethodGet {
		items, err := h.M.ListUsers()
		if err != nil {
			write(w, 500, map[string]string{"error": "query_failed"})
			return
		}
		out := make([]userResponseDTO, 0, len(items))
		for _, item := range items {
			out = append(out, userResponse(item))
		}
		write(w, 200, map[string]any{"items": out, "permissions": PermissionCatalog})
		return
	}
	if r.Method == http.MethodPost {
		if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
			write(w, 403, map[string]string{"error": "csrf"})
			return
		}
		var q AccountInput
		if err := decodeAuthBody(w, r, &q, accountRequestBodyLimit); err != nil {
			writeAuthDecodeError(w, err)
			return
		}
		created, err := h.M.CreateAccount(u.ID, q, time.Now().UTC())
		if err != nil {
			accountError(w, err)
			return
		}
		h.accountAudit(r, u, storage.User{}, created, "auth.account_create")
		write(w, 201, map[string]bool{"ok": true})
		return
	}
	w.Header().Set("Allow", "GET, POST")
	write(w, 405, map[string]string{"error": "method_not_allowed"})
}
func (h Handlers) User(w http.ResponseWriter, r *http.Request) {
	u, _, ok := h.current(r)
	if !ok {
		write(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	if !HasPermission(u, "accounts.manage") {
		write(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", http.MethodPut)
		write(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id == u.ID {
		write(w, 400, map[string]string{"error": "invalid_user"})
		return
	}
	if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
		write(w, 403, map[string]string{"error": "csrf"})
		return
	}
	var q AccountInput
	if err := decodeAuthBody(w, r, &q, accountRequestBodyLimit); err != nil {
		writeAuthDecodeError(w, err)
		return
	}
	before, after, err := h.M.UpdateAccount(u.ID, id, q, time.Now().UTC())
	if err != nil {
		accountError(w, err)
		return
	}
	h.accountAudit(r, u, before, after, "auth.account_update")
	write(w, 200, map[string]bool{"ok": true})
}
func (h Handlers) Password(w http.ResponseWriter, r *http.Request) {
	auditmeta.SetActor(r, "unknown", "human", "", "session")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		write(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	if h.M == nil {
		write(w, http.StatusServiceUnavailable, map[string]string{"error": "auth_unavailable"})
		return
	}
	u, c, ok := h.current(r)
	if !ok {
		write(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
		write(w, http.StatusForbidden, map[string]string{"error": "csrf"})
		return
	}
	var q struct {
		Old string `json:"old_password"`
		New string `json:"new_password"`
	}
	if err := decodeAuthBody(w, r, &q, authRequestBodyLimit); err != nil {
		writeAuthDecodeError(w, err)
		return
	}
	if h.M.ChangePassword(u.ID, q.Old, q.New, time.Now().UTC()) != nil {
		write(w, 401, map[string]string{"error": "invalid_credentials"})
		return
	}
	h.passwordAudit(r, u)
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
				if u.Role == "viewer" {
					if r.Method == http.MethodGet && r.URL.Path == "/api/dashboard/menu-visibility" {
						next.ServeHTTP(w, withUser(withActor(r, u.Username), u))
						return
					}
					passthrough := r.URL.Path == "/api/dashboard/passthrough/currency" || r.URL.Path == "/api/dashboard/passthrough/users" || r.URL.Path == "/api/dashboard/passthrough/logs" || r.URL.Path == "/api/dashboard/passthrough/logs/stat" || r.URL.Path == "/api/dashboard/passthrough/logs/count"
					if r.Method != http.MethodGet || (r.URL.Path != "/api/dashboard/metrics" && r.URL.Path != "/api/dashboard/metric-history" && r.URL.Path != "/api/dashboard/instances" && !passthrough) {
						write(w, 403, map[string]string{"error": "forbidden"})
						return
					}
					if r.URL.Path != "/api/dashboard/instances" && !passthrough && !strings.HasPrefix(r.URL.Query().Get("dimension_type"), "instance_user") {
						write(w, 403, map[string]string{"error": "forbidden"})
						return
					}
					q := r.URL.Query()
					q.Set("site", u.ScopeSite)
					q.Del("instance_id")
					r.URL.RawQuery = q.Encode()
				}
				if u.Role != "viewer" && !allowAdminRequest(u, r) {
					write(w, 403, map[string]string{"error": "forbidden"})
					return
				}
				next.ServeHTTP(w, withUser(withActor(r, u.Username), u))
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
