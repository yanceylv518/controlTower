package ingest

import (
	"testing"
	"time"

	"controltower/server/internal/storage"
)

func TestExpireDeliveriesForResolvedAlertsAllowsRenotification(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	alert := storage.Alert{ID: "alert-1", InstanceID: "inst-a", RuleKey: "recent_errors", Severity: "warning", Status: "firing", FirstSeenAt: now, LastSeenAt: now}
	if err := store.UpsertCurrentAlerts([]storage.Alert{alert}, now); err != nil {
		t.Fatalf("upsert alert: %v", err)
	}
	sent := storage.NotificationDelivery{ID: "delivery-1", AlertID: "alert-1", ChannelID: "chan-1", Status: "sent", AttemptedAt: now, NextAttemptAt: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), Attempts: 1}
	if err := store.InsertNotificationDelivery(sent); err != nil {
		t.Fatalf("insert delivery: %v", err)
	}

	due, err := store.NotificationDeliveryDue("alert-1", "chan-1", now)
	if err != nil || due {
		t.Fatalf("expected sent delivery to block renotification, due=%v err=%v", due, err)
	}

	// While the alert is still firing, expiry must not release the delivery.
	if err := store.ExpireDeliveriesForResolvedAlerts(now.Add(time.Minute)); err != nil {
		t.Fatalf("expire deliveries: %v", err)
	}
	due, err = store.NotificationDeliveryDue("alert-1", "chan-1", now.Add(time.Minute))
	if err != nil || due {
		t.Fatalf("expected firing alert delivery to stay blocked, due=%v err=%v", due, err)
	}

	resolvedAt := now.Add(2 * time.Minute)
	if err := store.ResolveMissingAlerts(nil, resolvedAt); err != nil {
		t.Fatalf("resolve alerts: %v", err)
	}
	if err := store.ExpireDeliveriesForResolvedAlerts(resolvedAt); err != nil {
		t.Fatalf("expire deliveries: %v", err)
	}

	due, err = store.NotificationDeliveryDue("alert-1", "chan-1", resolvedAt)
	if err != nil || !due {
		t.Fatalf("expected resolved alert delivery to become due again, due=%v err=%v", due, err)
	}
}
