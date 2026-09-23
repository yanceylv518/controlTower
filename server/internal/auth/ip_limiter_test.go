package auth

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
)

func TestLoginIPLimiterIndependentAndWindow(t *testing.T) {
	s := ingest.NewMemoryStore()
	now := time.Now().UTC()
	hash, _ := HashPassword("password1")
	_ = s.CreateUser(storage.User{Username: "admin", PasswordHash: hash, Role: "admin", Enabled: true, CreatedAt: now, UpdatedAt: now})
	m := NewManager(s, time.Hour)
	limiter := NewIPLimiter()
	clock := now
	limiter.now = func() time.Time { return clock }
	h := Handlers{M: m, Limiter: limiter}
	attempt := 0
	login := func(ip string) int {
		attempt++
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"missing`+strconv.Itoa(attempt)+`","password":"bad"}`))
		r.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		h.Login(w, r)
		return w.Code
	}
	for i := 0; i < 10; i++ {
		if code := login("10.0.0.1"); code != 401 {
			t.Fatalf("attempt %d=%d", i, code)
		}
	}
	if code := login("10.0.0.1"); code != 429 {
		t.Fatalf("11th=%d", code)
	}
	if code := login("10.0.0.2"); code != 401 {
		t.Fatalf("independent=%d", code)
	}
	clock = clock.Add(time.Minute + time.Second)
	if code := login("10.0.0.1"); code != 401 {
		t.Fatalf("after window=%d", code)
	}
}

func TestLoginIPLimiterBoundsDistinctAddressesAndCleansExpiredEntries(t *testing.T) {
	limiter := NewIPLimiter()
	clock := time.Now().UTC()
	limiter.now = func() time.Time { return clock }
	for index := 0; index < maxLoginIPEntries; index++ {
		if !limiter.Allow("2001:db8::" + strconv.FormatInt(int64(index), 16)) {
			t.Fatalf("address %d was rejected before the capacity limit", index)
		}
	}
	if limiter.Allow("198.51.100.1") {
		t.Fatal("new address was allowed after the limiter capacity was reached")
	}
	if got := len(limiter.entries); got > maxLoginIPEntries {
		t.Fatalf("IP limiter map exceeded limit: %d", got)
	}

	clock = clock.Add(time.Minute + time.Second)
	if !limiter.Allow("198.51.100.1") {
		t.Fatal("expired IP entries were not cleaned")
	}
	if got := len(limiter.entries); got != 1 {
		t.Fatalf("expected only the new address after cleanup, got %d", got)
	}
}
