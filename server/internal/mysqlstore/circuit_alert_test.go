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
	"github.com/stretchr/testify/require"
)

func TestCircuitImportMixedRulesAcrossBatches(t *testing.T) {
	s, site, now := lifelineStore(t)
	node := site + "-node"
	require.NoError(t, s.CreateInstance(storage.Instance{ID: node, SiteID: site, Name: "test", Enabled: true, CreatedAt: now, UpdatedAt: now}))
	// Ordinary weight decisions must not drive the event-import scan.
	digit := `(SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9)`
	_, err := s.db.Exec(`INSERT INTO tuning_recommendations(id,instance_id,channel_id,channel_name,created_at,rule,evidence_json,current_weight,proposed_weight,mode_at_creation,status) SELECT CONCAT(?,a.n, b.n,c.n,d.n),?,1,'test',?,'adjust','{}',100,101,'auto','auto_executed' FROM `+digit+` a CROSS JOIN `+digit+` b CROSS JOIN `+digit+` c CROSS JOIN `+digit+` d`, site+"-ordinary-", site, now)
	require.NoError(t, err)
	for i := 0; i < 501; i++ {
		id := fmt.Sprintf("%s-%04d", site, i)
		cmd := id + "-cmd"
		rule := "circuit_opened"
		if i%2 == 1 {
			rule = "circuit_recovered"
		}
		at := now.Add(time.Duration(i+1) * time.Millisecond)
		_, err = s.db.Exec(`INSERT INTO channel_commands(site_id,execution_kind,ownership_evidence,id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at) VALUES(?,'agent','recorded',?,?,?,'channel.update','{}','succeeded','test',?,?)`, site, cmd, node, i/2+1, at, at)
		require.NoError(t, err)
		require.NoError(t, s.InsertRecommendation(tuning.Recommendation{ID: id, InstanceID: site, ChannelID: int64(i/2 + 1), ChannelName: "test", Rule: rule, ModeAtCreation: "auto", Status: "auto_executed", CommandID: &cmd, CreatedAt: at, Evidence: map[string]any{}}))
	}
	start := time.Now()
	require.NoError(t, s.SyncCircuitAlerts(now.Add(time.Second)))
	t.Logf("import 500 mixed events with 10000 ordinary decisions: %s", time.Since(start))
	var count int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM circuit_alert_events WHERE site_id=?`, site).Scan(&count))
	require.Equal(t, 500, count)
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM alerts a JOIN circuit_alert_events e ON e.alert_id=a.id WHERE e.site_id=? AND a.status='firing'`, site).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, s.SyncCircuitAlerts(now.Add(time.Second)))
	require.NoError(t, s.SyncCircuitAlerts(now.Add(time.Second)))
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM circuit_alert_events WHERE site_id=?`, site).Scan(&count))
	require.Equal(t, 501, count)
}

func TestCircuitNotificationCutoffPreservesHistoryAndNewEvents(t *testing.T) {
	s, site, now := lifelineStore(t)
	var cutoff time.Time
	require.NoError(t, s.db.QueryRow(`SELECT occurred_at FROM circuit_alert_events WHERE event_id='__notification_cutoff__'`).Scan(&cutoff))
	node := site + "-node"
	require.NoError(t, s.CreateInstance(storage.Instance{ID: node, SiteID: site, Name: "test", Enabled: true, CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, s.UpsertNotificationChannel(storage.NotificationChannel{ID: site, SiteID: site, ChannelType: "webhook", RuleKeys: []string{"channel_circuit_opened", "channel_circuit_recovered"}, Enabled: true, CreatedAt: now, UpdatedAt: now}))
	for _, rule := range []string{"circuit_opened", "circuit_recovered"} {
		for _, historical := range []bool{true, false} {
			id := fmt.Sprintf("%s-%s-%t", site, rule, historical)
			at := cutoff
			if historical {
				at = cutoff.Add(-time.Millisecond)
			}
			a := circuitEventAlert(circuitEvent{ID: id, SiteID: site, InstanceID: node, Rule: rule, At: at})
			require.NoError(t, s.UpsertCurrentAlerts([]storage.Alert{a}, now))
			_, err := s.db.Exec(`INSERT INTO circuit_alert_events(event_id,alert_id,site_id,channel_id,rule_key,occurred_at) VALUES(?,?,?,1,?,?)`, id, a.ID, site, a.RuleKey, at)
			require.NoError(t, err)
			require.NoError(t, s.InsertNotificationDelivery(storage.NotificationDelivery{ID: a.ID, AlertID: a.ID, ChannelID: site, Status: "failed", AttemptedAt: now.Add(-time.Minute), NextAttemptAt: now.Add(-time.Second), Attempts: 2}))
			due, err := New(s.db).NotificationDeliveryDue(a.ID, site, now)
			require.NoError(t, err)
			require.Equal(t, !historical, due)
		}
	}
	alerts, err := New(s.db).CircuitNotificationAlerts(now)
	require.NoError(t, err)
	var n int
	for _, a := range alerts {
		if a.InstanceID == node {
			n++
			require.False(t, a.FirstSeenAt.Before(cutoff))
		}
	}
	require.Equal(t, 2, n)
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM alerts WHERE instance_id=?`, node).Scan(&n))
	require.Equal(t, 4, n) // Suppression must not delete history.
	// An old decision confirmed only after upgrade must not become a new
	// notification merely because its command acknowledgement is recent.
	legacy := site + "-late-old"
	_, err = s.db.Exec(`INSERT INTO channel_commands(site_id,execution_kind,ownership_evidence,id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at) VALUES(?,'agent','recorded',?,?,2,'channel.update','{}','succeeded','test',?,?)`, site, legacy, node, cutoff.Add(-time.Millisecond), now)
	require.NoError(t, err)
	require.NoError(t, s.InsertRecommendation(tuning.Recommendation{ID: legacy, InstanceID: site, ChannelID: 2, Rule: "circuit_opened", ModeAtCreation: "auto", Status: "auto_executed", CommandID: &legacy, CreatedAt: cutoff.Add(-time.Millisecond)}))
	require.NoError(t, New(s.db).SyncCircuitAlerts(now))
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM circuit_alert_events WHERE event_id=?`, legacy).Scan(&n))
	require.Zero(t, n)
	// The migration and process restarts must not move the boundary forward.
	require.NoError(t, ApplyDir(context.Background(), s.db, "../../migrations"))
	var after time.Time
	require.NoError(t, s.db.QueryRow(`SELECT occurred_at FROM circuit_alert_events WHERE event_id='__notification_cutoff__'`).Scan(&after))
	require.True(t, cutoff.Equal(after))
}

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

func TestCircuitEventAlertDescribesConfirmedAction(t *testing.T) {
	for _, tc := range []struct {
		name, rule, evidence string
		want, absent         []string
	}{
		{
			name: "regular circuit", rule: "circuit_opened", evidence: `{}`,
			want:   []string{"渠道权重已置零", "优先级保持不变", "完整一轮探针全部失败时禁用渠道", "禁用后继续定期探测"},
			absent: []string{"渠道已禁用", "优先级已置零", "已重新启用"},
		},
		{
			name: "fast circuit", rule: "circuit_opened", evidence: `{"trigger":"agent_report_batch","error_rate":0.6}`,
			want:   []string{"渠道权重已置零", "优先级保持不变", "上报批次快速熔断", "60.0%"},
			absent: []string{"优先级已置零", "渠道已禁用"},
		},
		{
			name: "weight recovery", rule: "circuit_recovered", evidence: `{}`,
			want:   []string{"软启动权重已写入", "优先级保持不变", "后续权重按调权策略评估"},
			absent: []string{"已重新启用", "恢复全部流量"},
		},
		{
			name: "disabled channel recovery", rule: "circuit_recovered", evidence: `{"channel_status":1,"secret":"must-not-leak"}`,
			want:   []string{"渠道已重新启用", "软启动权重已写入", "优先级保持不变"},
			absent: []string{"must-not-leak", "恢复全部流量"},
		},
		{
			name: "invalid evidence", rule: "circuit_recovered", evidence: `{"channel_status":1,`,
			want: []string{"软启动权重已写入"}, absent: []string{"已重新启用"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := circuitEventAlert(circuitEvent{ID: tc.name, Rule: tc.rule, Evidence: tc.evidence})
			for _, want := range tc.want {
				if !strings.Contains(a.Summary, want) {
					t.Fatalf("missing %q in %s", want, a.Summary)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(a.Summary, absent) {
					t.Fatalf("misleading or unsafe %q in %s", absent, a.Summary)
				}
			}
			if tc.rule == "circuit_recovered" && !strings.Contains(a.Title, "软启动") {
				t.Fatalf("recovery title must identify soft start: %s", a.Title)
			}
		})
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
		_, err = db.Exec(`INSERT INTO channel_commands(site_id,execution_kind,ownership_evidence,id,instance_id,channel_id,command_type,payload_json,status,created_by,error_summary,created_at,updated_at) VALUES(?,'agent','recorded',?,?,12,'channel.update','{}',?,'system:auto','',?,?)`, site, cmd, node, status, at, at)
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
	current, err := s.OverviewAlerts(context.Background(), site, now.Add(time.Second))
	must(err)
	if current.Total != 1 || current.Unconfirmed != 0 || len(current.Items) != 1 || current.Items[0].DimensionKey != site+":channel:12" {
		t.Fatalf("fresh circuit execution missing from overview: %+v", current)
	}
	stale, err := s.OverviewAlerts(context.Background(), site, now.Add(3*time.Minute))
	must(err)
	if stale.Unconfirmed != 1 || len(stale.Items) != 0 {
		t.Fatalf("historical circuit event treated as current: %+v", stale)
	}
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
