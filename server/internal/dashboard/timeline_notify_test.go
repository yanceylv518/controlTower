package dashboard

import (
	"bytes"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestRepeatedFiringDoesNotDuplicateEvent(t *testing.T) {
	s := ingest.NewMemoryStore()
	n := time.Now()
	a := storage.Alert{ID: "a", Status: "firing"}
	_ = s.UpsertCurrentAlerts([]storage.Alert{a}, n)
	_ = s.UpsertCurrentAlerts([]storage.Alert{a}, n.Add(time.Minute))
	events, _ := s.QueryAlertEvents("a", 100)
	if len(events) != 1 || events[0].EventType != "firing" {
		t.Fatalf("events=%v", events)
	}
}
func TestAlertActionActorFromToken(t *testing.T) {
	s := ingest.NewMemoryStore()
	n := time.Now()
	_ = s.UpsertCurrentAlerts([]storage.Alert{{ID: "a", Status: "firing"}}, n)
	h := NewHandler(s).WithAlertStore(s)
	wrapped := ctauth.RequireSessionOrToken(nil, "legacy", http.HandlerFunc(h.HandleAlertAction))
	r := httptest.NewRequest("POST", "/api/dashboard/alerts/action", bytes.NewBufferString(`{"id":"a","action":"acknowledge","note":"e2e"}`))
	r.Header.Set("Authorization", "Bearer legacy")
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	events, _ := s.QueryAlertEvents("a", 100)
	last := events[len(events)-1]
	if last.Actor != "token" || last.Note != "e2e" {
		t.Fatalf("event=%+v", last)
	}
}
func TestAlertActionActorFromSession(t *testing.T) {
	s := ingest.NewMemoryStore()
	n := time.Now()
	hash, _ := ctauth.HashPassword("password1")
	_ = s.CreateUser(storage.User{Username: "admin", PasswordHash: hash, Role: "admin", CreatedAt: n, UpdatedAt: n})
	m := ctauth.NewManager(s, time.Hour)
	_, session, e := m.Login("admin", "password1", n)
	if e != nil {
		t.Fatal(e)
	}
	_ = s.UpsertCurrentAlerts([]storage.Alert{{ID: "a", Status: "firing"}}, n)
	h := NewHandler(s).WithAlertStore(s)
	wrapped := ctauth.RequireSessionOrToken(m, "legacy", http.HandlerFunc(h.HandleAlertAction))
	r := httptest.NewRequest("POST", "/api/dashboard/alerts/action", bytes.NewBufferString(`{"id":"a","action":"acknowledge"}`))
	r.AddCookie(&http.Cookie{Name: "ct_session", Value: session.ID})
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	events, _ := s.QueryAlertEvents("a", 100)
	if events[len(events)-1].Actor != "admin" {
		t.Fatalf("events=%v", events)
	}
}

func TestAlertTimelineAscendingAndLimit(t *testing.T) {
	s := ingest.NewMemoryStore()
	n := time.Now()
	_ = s.InsertAlertEvents([]storage.AlertEvent{{AlertID: "a", EventType: "firing", Actor: "system", CreatedAt: n}, {AlertID: "a", EventType: "acknowledged", Actor: "admin", Note: "ok", CreatedAt: n.Add(time.Second)}})
	h := NewHandler(s).WithAlertStore(s)
	r := httptest.NewRequest("GET", "/api/dashboard/alerts/a/events?limit=1", nil)
	r.SetPathValue("id", "a")
	w := httptest.NewRecorder()
	h.HandleAlertEvents(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"event_type":"firing"`) || strings.Contains(w.Body.String(), "acknowledged") {
		t.Fatal(w.Body.String())
	}
}
func TestDingTalkSigningAndSecretMask(t *testing.T) {
	n := time.UnixMilli(1700000000000)
	signed := dingTalkSignedURL("https://example.com/hook?access_token=x", "secret", n)
	u, e := url.Parse(signed)
	if e != nil || u.Query().Get("timestamp") != "1700000000000" || u.Query().Get("sign") == "" {
		t.Fatal(signed)
	}
	items := notificationChannelItems([]storage.NotificationChannel{{ID: "c", SecretValue: "secret", WebhookURL: "https://example.com/very-long-webhook"}})
	if !items[0].HasSecret {
		t.Fatal("missing has_secret")
	}
}
func TestExhaustedNotDueAndResend(t *testing.T) {
	s := ingest.NewMemoryStore()
	n := time.Now()
	d := storage.NotificationDelivery{ID: "d", AlertID: "a", ChannelID: "c", Status: "exhausted", NextAttemptAt: n.Add(-time.Hour), Attempts: 8}
	_ = s.InsertNotificationDelivery(d)
	if due, _ := s.NotificationDeliveryDue("a", "c", n); due {
		t.Fatal("exhausted due")
	}
	ok, e := s.MarkDeliveryForResend("a:c", n)
	if e != nil || !ok {
		t.Fatalf("resend %v %v", ok, e)
	}
}
