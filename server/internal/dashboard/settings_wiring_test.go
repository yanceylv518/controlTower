package dashboard

import (
	"testing"
	"time"

	"controltower/server/internal/aggregator"
	"controltower/server/internal/settings"
	"controltower/server/internal/storage"
)

func TestAlertThresholdWiringUsesProvidedValues(t *testing.T) {
	now := time.Now().UTC()
	errorRate := 0.15
	p95 := 3.0
	values := defaultAlertSettings()
	values.ErrorRateWarn = 10
	values.ErrorRateCrit = 20
	values.P95Warn = 2
	values.P95Crit = 4
	values.CPUWarn = 50
	values.CPUCrit = 60
	items := BuildCurrentAlertsWithSettings([]aggregator.Metric{{InstanceID: "i", DimensionKey: "i", RequestCount: 10, ErrorCount: 2, ErrorRate: &errorRate, P95UseTime: &p95, BucketTime: now}}, []storage.ServerMetric{{InstanceID: "i", CPUPercent: 55, CollectedAt: now}}, nil, nil, values)
	seen := map[string]string{}
	for _, item := range items {
		seen[item.RuleKey] = item.Severity
	}
	if seen["high_error_rate"] != "warning" || seen["high_p95_latency"] != "warning" || seen["high_cpu"] != "warning" {
		t.Fatalf("settings not applied: %#v", seen)
	}
}

type dashboardSettingsStore struct{ values map[string]string }

func (s *dashboardSettingsStore) ListSystemSettings() ([]storage.SystemSetting, error) {
	out := []storage.SystemSetting{}
	for k, v := range s.values {
		out = append(out, storage.SystemSetting{Key: k, Value: v})
	}
	return out, nil
}
func (s *dashboardSettingsStore) ReplaceSystemSettings(v map[string]string, _ string, _ time.Time) error {
	s.values = v
	return nil
}

func TestOfflineThresholdWiring(t *testing.T) {
	now := time.Now().UTC()
	items := summarizeAgentsWithThreshold([]storage.Agent{{ID: "a", LastSeenAt: now.Add(-90 * time.Second)}}, now, 60)
	if items[0].Online {
		t.Fatal("agent should be offline with configured threshold")
	}
}

func TestInstanceOfflineAlertBranches(t *testing.T) {
	now := time.Now().UTC()
	instances := []storage.Instance{{ID: "offline", Enabled: true}, {ID: "never", Enabled: true}, {ID: "recovered", Enabled: true}, {ID: "retired", Enabled: true}, {ID: "disabled", Enabled: false}}
	agents := []storage.Agent{{InstanceID: "offline", LastSeenAt: now.Add(-3 * time.Minute)}, {InstanceID: "recovered", LastSeenAt: now.Add(-30 * time.Second)}, {InstanceID: "retired", LastSeenAt: now.Add(-8 * 24 * time.Hour)}, {InstanceID: "disabled", LastSeenAt: now.Add(-3 * time.Minute)}}
	items := appendInstanceOfflineAlerts(nil, instances, agents, now, 120)
	if len(items) != 1 || items[0].InstanceID != "offline" || items[0].Severity != "critical" || items[0].Title != "实例离线" {
		t.Fatalf("unexpected offline alerts: %#v", items)
	}
}

func TestNotificationMasterSwitchWiring(t *testing.T) {
	store := &dashboardSettingsStore{values: map[string]string{settings.NotificationsEnabled: "false"}}
	provider := settings.NewProvider(store, time.Hour)
	h := Handler{settings: provider}
	if err := h.dispatchAlertNotifications([]storage.Alert{{ID: "a", Status: "firing"}}); err != nil {
		t.Fatal(err)
	}
}
