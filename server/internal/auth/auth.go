package auth

import (
	"context"
	"controltower/server/internal/storage"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

const iterations = 600000

const (
	maxLoginPasswordBytes    = 1024
	maxTrackedLoginAttempts  = 8192
	loginFailureThreshold    = 5
	loginAttemptLockDuration = 10 * time.Minute
	loginAttemptRetention    = 10 * time.Minute
	loginAttemptCleanupEvery = time.Minute
)

var ErrInvalid = errors.New("invalid credentials")
var ErrLocked = errors.New("locked")

type Store interface {
	UserByUsername(string) (storage.User, bool, error)
	UserByID(int64) (storage.User, bool, error)
	CreateUser(storage.User) error
	UpdateUserPassword(int64, string, time.Time) error
	ListUsers() ([]storage.User, error)
	UpdateUser(storage.User) error
	CountUsers() (int, error)
	CreateSession(storage.Session) error
	SessionByID(string) (storage.Session, bool, error)
	DeleteSession(string) error
	DeleteExpiredSessions(time.Time) (int, error)
}
type attempt struct {
	failures    int
	until       time.Time
	lastFailure time.Time
}
type Manager struct {
	store              Store
	ttl                time.Duration
	mu                 sync.Mutex
	attempts           map[string]attempt
	nextAttemptCleanup time.Time
}

func NewManager(s Store, ttl time.Duration) *Manager {
	return &Manager{store: s, ttl: ttl, attempts: map[string]attempt{}}
}
func HashPassword(p string) (string, error) {
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	key, e := pbkdf2.Key(sha256.New, p, salt, iterations, 32)
	if e != nil {
		return "", e
	}
	return "pbkdf2$sha256$" + strconv.Itoa(iterations) + "$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func VerifyPassword(v, p string) bool {
	f := strings.Split(v, "$")
	if len(f) != 5 || f[0] != "pbkdf2" || f[1] != "sha256" {
		return false
	}
	i, e := strconv.Atoi(f[2])
	if e != nil || i < 1 {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(f[3])
	if e != nil {
		return false
	}
	want, e := base64.RawStdEncoding.DecodeString(f[4])
	if e != nil || len(want) == 0 {
		return false
	}
	got, e := pbkdf2.Key(sha256.New, p, salt, i, len(want))
	return e == nil && subtle.ConstantTimeCompare(got, want) == 1
}
func (m *Manager) Login(name, p string, now time.Time) (storage.User, storage.Session, error) {
	if strings.TrimSpace(name) == "" {
		return storage.User{}, storage.Session{}, ErrInvalid
	}
	if m.loginLocked(name, now) {
		return storage.User{}, storage.Session{}, ErrLocked
	}
	u, ok, e := m.store.UserByUsername(name)
	if e != nil {
		return u, storage.Session{}, e
	}
	if !ok || !u.Enabled {
		return u, storage.Session{}, ErrInvalid
	}
	if !VerifyPassword(u.PasswordHash, p) {
		m.recordLoginFailure(name, now)
		return u, storage.Session{}, ErrInvalid
	}
	m.mu.Lock()
	delete(m.attempts, name)
	m.mu.Unlock()
	b := make([]byte, 32)
	if _, e = rand.Read(b); e != nil {
		return u, storage.Session{}, e
	}
	s := storage.Session{ID: hex.EncodeToString(b), UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(m.ttl)}
	e = m.store.CreateSession(s)
	return u, s, e
}

func (m *Manager) loginLocked(name string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLoginAttemptsLocked(now)
	a, ok := m.attempts[name]
	if !ok {
		return false
	}
	if now.Before(a.until) {
		return true
	}
	if !a.until.IsZero() || !now.Before(a.lastFailure.Add(loginAttemptRetention)) {
		delete(m.attempts, name)
	}
	return false
}

func (m *Manager) recordLoginFailure(name string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLoginAttemptsLocked(now)
	a, exists := m.attempts[name]
	if !exists && len(m.attempts) >= maxTrackedLoginAttempts {
		// 不为任意新用户名分配状态，避免未认证输入耗尽内存。
		return
	}
	if !a.until.IsZero() || (!a.lastFailure.IsZero() && !now.Before(a.lastFailure.Add(loginAttemptRetention))) {
		a = attempt{}
	}
	a.failures++
	a.lastFailure = now
	if a.failures >= loginFailureThreshold {
		a.until = now.Add(loginAttemptLockDuration)
	}
	m.attempts[name] = a
}

func (m *Manager) cleanupLoginAttemptsLocked(now time.Time) {
	if !m.nextAttemptCleanup.IsZero() && now.Before(m.nextAttemptCleanup) {
		return
	}
	m.nextAttemptCleanup = now.Add(loginAttemptCleanupEvery)
	for name, value := range m.attempts {
		if !now.Before(value.lastFailure.Add(loginAttemptRetention)) {
			delete(m.attempts, name)
		}
	}
}
func (m *Manager) Validate(id string, now time.Time) (storage.User, bool) {
	s, ok, e := m.store.SessionByID(id)
	if e != nil || !ok || !s.ExpiresAt.After(now) {
		return storage.User{}, false
	}
	u, ok, e := m.store.UserByID(s.UserID)
	return u, ok && e == nil && u.Enabled
}

func (m *Manager) ListUsers() ([]storage.User, error) { return m.store.ListUsers() }
func (m *Manager) CreateScopedUser(username, password, role, site string, userIDs []int64, now time.Time) error {
	if strings.TrimSpace(username) == "" || len(password) < 8 || len(password) > maxLoginPasswordBytes || (role != "admin" && role != "viewer") {
		return ErrInvalid
	}
	if role == "viewer" && (strings.TrimSpace(site) == "" || len(userIDs) == 0) {
		return ErrInvalid
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return m.store.CreateUser(storage.User{Username: strings.TrimSpace(username), PasswordHash: hash, Role: role, ScopeSite: strings.TrimSpace(site), ScopeUserIDs: userIDs, Enabled: true, CreatedAt: now, UpdatedAt: now})
}
func (m *Manager) UpdateScopedUser(id int64, role, site string, userIDs []int64, enabled bool, now time.Time) error {
	u, ok, err := m.store.UserByID(id)
	if err != nil || !ok {
		return ErrInvalid
	}
	if role != "admin" && role != "viewer" {
		return ErrInvalid
	}
	if role == "viewer" && (strings.TrimSpace(site) == "" || len(userIDs) == 0) {
		return ErrInvalid
	}
	u.Role, u.ScopeSite, u.ScopeUserIDs, u.Enabled, u.UpdatedAt = role, strings.TrimSpace(site), userIDs, enabled, now
	return m.store.UpdateUser(u)
}
func (m *Manager) Logout(id string) error { return m.store.DeleteSession(id) }
func (m *Manager) ChangePassword(id int64, old, n string, now time.Time) error {
	if len(n) < 8 || len(n) > maxLoginPasswordBytes {
		return ErrInvalid
	}
	u, ok, e := m.store.UserByID(id)
	if e != nil || !ok || !VerifyPassword(u.PasswordHash, old) {
		return ErrInvalid
	}
	h, e := HashPassword(n)
	if e != nil {
		return e
	}
	return m.store.UpdateUserPassword(id, h, now)
}
func (m *Manager) CleanupLoop(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case n := <-t.C:
			_, _ = m.store.DeleteExpiredSessions(n)
		}
	}
}
func (m *Manager) TTL() time.Duration { return m.ttl }
