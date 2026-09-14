package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
)

func TestCircuitNotificationsRespectSiteCategoryAndRecoveryDedup(t *testing.T) {
	count := 0
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count++; w.WriteHeader(200) }))
	defer hook.Close()
	s := ingest.NewMemoryStore()
	now := time.Now().UTC()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.CreateInstance(storage.Instance{ID: "node", SiteID: "site", Enabled: true}))
	for _, ch := range []storage.NotificationChannel{
		{ID: "correct", SiteID: "site", RuleKeys: []string{"channel_circuit_opened", "channel_circuit_recovered"}},
		{ID: "other-site", SiteID: "other", RuleKeys: []string{"channel_circuit_opened", "channel_circuit_recovered"}},
		{ID: "system-only", SiteID: "site", RuleKeys: []string{"high_cpu"}},
	} {
		ch.Enabled = true
		ch.ChannelType = "webhook"
		ch.WebhookURL = hook.URL
		must(s.UpsertNotificationChannel(ch))
	}
	h := NewHandler(s).WithNotificationStore(s).WithInstanceStore(s)
	open := storage.Alert{ID: "open", InstanceID: "node", RuleKey: "channel_circuit_opened", Status: "firing", FirstSeenAt: now, LastSeenAt: now}
	must(s.UpsertCurrentAlerts([]storage.Alert{open}, now))
	must(h.dispatchAlertNotifications([]storage.Alert{open}))
	must(h.dispatchAlertNotifications([]storage.Alert{open}))
	if count != 1 {
		t.Fatalf("open delivery count %d", count)
	}
	must(s.ResolveMissingAlerts(nil, now))
	alerts, err := s.QueryAlerts(storage.AlertQuery{})
	must(err)
	if alerts[0].Status != "firing" {
		t.Fatal("false resolution")
	}
	must(s.UpdateAlertAction(open.ID, "resolved", nil, now))
	recovery := storage.Alert{ID: "recovery", InstanceID: "node", RuleKey: "channel_circuit_recovered", Status: "resolved", FirstSeenAt: now, LastSeenAt: now}
	must(s.UpsertCurrentAlerts([]storage.Alert{recovery}, now))
	must(h.dispatchAlertNotifications([]storage.Alert{recovery}))
	must(h.dispatchAlertNotifications([]storage.Alert{recovery}))
	if count != 2 {
		t.Fatalf("recovery delivery count %d", count)
	}
	due, err := s.NotificationDeliveryDue(open.ID, "correct", now)
	must(err)
	if due {
		t.Fatal("old open released after recovery")
	}
}
