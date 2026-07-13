package dashboard

import (
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

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
