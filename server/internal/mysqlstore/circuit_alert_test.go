package mysqlstore

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
)

func TestCircuitEventAlertIdentityAndRecovery(t *testing.T) {
	e := circuitEvent{ID: "event-a", SiteID: "site-a", InstanceID: "node-a", Name: "channel", ChannelID: 12, Rule: "circuit_opened", At: time.Now(), Evidence: `{"trigger":"agent_report_batch","error_rate":0.6,"secret":"must-not-leak"}`}
	a := circuitEventAlert(e)
	if a.Status != "firing" || a.RuleKey != "channel_circuit_opened" || !strings.Contains(a.Summary, "60.0%") || strings.Contains(a.Summary, "must-not-leak") {
		t.Fatal(a)
	}
	if circuitEventAlert(e).ID != a.ID {
		t.Fatal("unstable event identity")
	}
	e.ID = "event-b"
	e.Rule = "circuit_recovered"
	recovered := circuitEventAlert(e)
	if recovered.ID == a.ID || recovered.Status != "resolved" || recovered.ResolvedAt == nil {
		t.Fatal(recovered)
	}
}

func TestMySQLCircuitAlertLifecycle(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires local CT_MYSQL_TEST_DSN")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = ApplyDir(context.Background(), db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	now := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	site := fmt.Sprintf("circuit-test-%d", now.UnixNano())
	node := site + "-n"
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.CreateInstance(storage.Instance{ID: node, SiteID: site, Name: "test", Enabled: true, CreatedAt: now, UpdatedAt: now}))
	defer func() {
		for _, q := range []string{
			"DELETE d FROM notification_deliveries d JOIN alerts a ON a.id=d.alert_id WHERE a.instance_id=?",
			"DELETE e FROM alert_events e JOIN alerts a ON a.id=e.alert_id WHERE a.instance_id=?",
			"DELETE FROM alerts WHERE instance_id=?", "DELETE FROM channel_commands WHERE instance_id=?", "DELETE FROM instances WHERE id=?"} {
			_, _ = db.Exec(q, node)
		}
		_, _ = db.Exec("DELETE FROM circuit_alert_events WHERE site_id=?", site)
		_, _ = db.Exec("DELETE FROM tuning_recommendations WHERE instance_id=?", site)
		_, _ = db.Exec("DELETE FROM notification_channels WHERE site_id=?", site)
	}()
	seed := func(suffix, rule, mode, status string, at time.Time) string {
		t.Helper()
		id := site + suffix
		cmd := id + "-cmd"
		_, err = db.Exec(`INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,error_summary,created_at,updated_at) VALUES(?,?,12,'channel.update','{}',?,'system:auto','',?,?)`, cmd, node, status, at, at)
		must(err)
		must(s.InsertRecommendation(tuning.Recommendation{ID: id, InstanceID: site, ChannelID: 12, ChannelName: "test channel", Rule: rule, ModeAtCreation: mode, Status: "auto_executed", CommandID: &cmd, CreatedAt: at, Evidence: map[string]any{}}))
		return cmd
	}
	seed("-old", "circuit_opened", "auto", "succeeded", now.Add(-24*time.Hour))
	seed("-observe", "circuit_opened", "observe", "succeeded", now)
	seed("-failed", "circuit_opened", "auto", "failed", now)
	pending := seed("-pending", "circuit_opened", "auto", "pending", now)
	must(s.SyncCircuitAlerts(now.Add(time.Second)))
	alerts, err := s.QueryAlerts(storage.AlertQuery{InstanceID: node})
	must(err)
	if len(alerts) != 0 {
		t.Fatalf("unconfirmed/historical events imported: %+v", alerts)
	}
	_, err = db.Exec("UPDATE channel_commands SET status='succeeded' WHERE id=?", pending)
	must(err)
	must(s.SyncCircuitAlerts(now.Add(time.Second)))
	must(s.SyncCircuitAlerts(now.Add(time.Second))) // restart-safe replay
	alerts, err = s.QueryAlerts(storage.AlertQuery{InstanceID: node})
	must(err)
	if len(alerts) != 1 || alerts[0].Status != "firing" {
		t.Fatalf("expected one open: %+v", alerts)
	}
	opened := alerts[0]
	must(s.ResolveMissingAlerts(nil, now))
	alerts, err = s.QueryAlerts(storage.AlertQuery{InstanceID: node})
	must(err)
	if alerts[0].Status != "firing" {
		t.Fatal("metric scan falsely resolved circuit")
	}
	must(s.UpsertNotificationChannel(storage.NotificationChannel{ID: site, SiteID: site, ChannelType: "webhook", Name: "test", WebhookURL: "http://127.0.0.1:1", RuleKeys: []string{"channel_circuit_opened", "channel_circuit_recovered"}, Enabled: true, CreatedAt: now, UpdatedAt: now}))
	due, err := s.CircuitNotificationAlerts(now)
	must(err)
	found := false
	for _, a := range due {
		if a.ID == opened.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("open missing from due events")
	}
	must(s.InsertNotificationDelivery(storage.NotificationDelivery{ID: opened.ID + "-delivery", AlertID: opened.ID, ChannelID: site, Status: "sent", AttemptedAt: now, NextAttemptAt: now, Attempts: 1}))
	seed("-recovery", "circuit_recovered", "auto", "succeeded", now.Add(2*time.Second))
	must(s.SyncCircuitAlerts(now.Add(3 * time.Second)))
	must(s.ExpireDeliveriesForResolvedAlerts(now.Add(3 * time.Second)))
	alerts, err = s.QueryAlerts(storage.AlertQuery{InstanceID: node})
	must(err)
	if len(alerts) != 2 {
		t.Fatal(alerts)
	}
	for _, a := range alerts {
		if a.Status != "resolved" {
			t.Fatal(a)
		}
	}
	isDue, err := s.NotificationDeliveryDue(opened.ID, site, now.Add(3*time.Second))
	must(err)
	if isDue {
		t.Fatal("recovery re-enabled the already sent open event")
	}
	due, err = s.CircuitNotificationAlerts(now.Add(3 * time.Second))
	must(err)
	var recovery storage.Alert
	for _, a := range due {
		if a.InstanceID == node {
			if a.RuleKey != "channel_circuit_recovered" {
				t.Fatal(a)
			}
			recovery = a
		}
	}
	if recovery.ID == "" {
		t.Fatal("recovery notification missing")
	}
	must(s.InsertNotificationDelivery(storage.NotificationDelivery{ID: recovery.ID + "-delivery", AlertID: recovery.ID, ChannelID: site, Status: "failed", AttemptedAt: now, NextAttemptAt: now.Add(time.Minute), Attempts: 1}))
	due, err = s.CircuitNotificationAlerts(now.Add(4 * time.Second))
	must(err)
	for _, a := range due {
		if a.ID == recovery.ID {
			t.Fatal("retry backoff ignored")
		}
	}
	due, err = s.CircuitNotificationAlerts(now.Add(2 * time.Minute))
	must(err)
	found = false
	for _, a := range due {
		if a.ID == recovery.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("recovery retry lost")
	}
	must(s.InsertNotificationDelivery(storage.NotificationDelivery{ID: recovery.ID + "-delivery", AlertID: recovery.ID, ChannelID: site, Status: "sent", AttemptedAt: now, NextAttemptAt: now, Attempts: 2}))
	must(s.ExpireDeliveriesForResolvedAlerts(now.Add(2 * time.Minute)))
	due, err = s.CircuitNotificationAlerts(now.Add(2 * time.Minute))
	must(err)
	for _, a := range due {
		if a.InstanceID == node {
			t.Fatal("sent circuit event repeated", a)
		}
	}
	seed("-again", "circuit_opened", "auto", "succeeded", now.Add(4*time.Second))
	must(s.SyncCircuitAlerts(now.Add(5 * time.Second)))
	due, err = s.CircuitNotificationAlerts(now.Add(5 * time.Second))
	must(err)
	found = false
	for _, a := range due {
		if a.InstanceID == node {
			if a.ID == opened.ID || a.Status != "firing" {
				t.Fatal(a)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("new circuit episode did not notify")
	}
}
