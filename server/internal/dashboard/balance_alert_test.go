package dashboard

import (
	"context"
	"testing"
	"time"

	"controltower/server/internal/settings"
	"controltower/server/internal/storage"
)

type balanceTestInstances struct {
	InstanceStore
	items []storage.Instance
}

func (s balanceTestInstances) ListInstances() ([]storage.Instance, error) { return s.items, nil }

type balanceTestSource struct{ users map[string][]PassthroughUser }

func (s balanceTestSource) ListUserBalances(_ context.Context, site string) ([]PassthroughUser, error) {
	return s.users[site], nil
}

type balanceTestUsage struct{ rows []storage.UserQuotaUsage }

func (s balanceTestUsage) QueryUserQuotaUsage(context.Context, time.Time) ([]storage.UserQuotaUsage, error) {
	return s.rows, nil
}

type balanceTestSettingsStore struct {
	items map[int64]storage.BalanceAlertUserSetting
}

func (s balanceTestSettingsStore) ListBalanceAlertUserSettings(context.Context, string) (map[int64]storage.BalanceAlertUserSetting, error) {
	return s.items, nil
}
func (s balanceTestSettingsStore) PutBalanceAlertUserSetting(context.Context, storage.BalanceAlertUserSetting) error {
	return nil
}

func balanceTestSettings(t *testing.T) settings.Values {
	t.Helper()
	items := map[string]settings.Item{}
	for _, key := range settings.Keys() {
		items[key] = settings.Item{Value: settings.DefaultValue(key)}
	}
	v, err := settings.Parse(items)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestBalanceAlertsUseRecentConsumptionRunway(t *testing.T) {
	values := balanceTestSettings(t)
	h := Handler{
		instanceStore:   balanceTestInstances{items: []storage.Instance{{ID: "node-1", SiteID: "site-a", Enabled: true}}},
		balanceSource:   balanceTestSource{users: map[string][]PassthroughUser{"site-a": {{ID: 7, Username: "alice", Quota: 1_000, Status: 1}}}},
		balanceUsage:    balanceTestUsage{rows: []storage.UserQuotaUsage{{InstanceID: "node-1", DimensionKey: "node-1:user:7", RequestCount: 30, Quota: 1_500}}},
		balanceSettings: balanceTestSettingsStore{items: map[int64]storage.BalanceAlertUserSetting{7: {UserID: 7, Enabled: true}}},
	}
	alerts, err := h.balanceAlerts(values, time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || alerts[0].Severity != "critical" || alerts[0].RuleKey != "user_low_balance" {
		t.Fatalf("unexpected alerts: %#v", alerts)
	}
}

type balanceFailingSource struct{ err error }

func (s balanceFailingSource) ListUserBalances(context.Context, string) ([]PassthroughUser, error) {
	return nil, s.err
}

type balanceForbiddenSource struct{ t *testing.T }

func (s balanceForbiddenSource) ListUserBalances(context.Context, string) ([]PassthroughUser, error) {
	s.t.Fatal("ListUserBalances must not be called when no user is enrolled")
	return nil, nil
}

// A failing readonly connection on one site must degrade to that site's
// previously cached alerts instead of failing the whole alert cycle.
func TestBalanceAlertsDegradePerSiteOnReadonlyFailure(t *testing.T) {
	values := balanceTestSettings(t)
	cache := &balanceAlertCache{items: []AlertItem{
		{ID: "cached", InstanceID: "node-1", RuleKey: "user_low_balance", Severity: "warning"},
		{ID: "other-site", InstanceID: "node-9", RuleKey: "user_low_balance", Severity: "critical"},
	}}
	h := Handler{
		instanceStore:   balanceTestInstances{items: []storage.Instance{{ID: "node-1", Enabled: true}}},
		balanceSource:   balanceFailingSource{err: context.DeadlineExceeded},
		balanceUsage:    balanceTestUsage{},
		balanceSettings: balanceTestSettingsStore{items: map[int64]storage.BalanceAlertUserSetting{7: {UserID: 7, Enabled: true}}},
		balanceCache:    cache,
	}
	alerts, err := h.balanceAlerts(values, time.Now().UTC())
	if err != nil {
		t.Fatalf("site failure must not fail the cycle: %v", err)
	}
	if len(alerts) != 1 || alerts[0].ID != "cached" {
		t.Fatalf("expected the site's cached alert to carry over, got %#v", alerts)
	}
}

// Sites without any enrolled user must not pay a readonly users-table scan.
func TestBalanceAlertsSkipReadonlyQueryWithoutEnrolledUsers(t *testing.T) {
	values := balanceTestSettings(t)
	h := Handler{
		instanceStore:   balanceTestInstances{items: []storage.Instance{{ID: "node-1", Enabled: true}}},
		balanceSource:   balanceForbiddenSource{t: t},
		balanceUsage:    balanceTestUsage{},
		balanceSettings: balanceTestSettingsStore{items: map[int64]storage.BalanceAlertUserSetting{7: {UserID: 7, Enabled: false}}},
	}
	alerts, err := h.balanceAlerts(values, time.Now().UTC())
	if err != nil || len(alerts) != 0 {
		t.Fatalf("alerts=%#v err=%v", alerts, err)
	}
}

func TestBalanceAlertsRequireEnabledUserAndMinimumSamples(t *testing.T) {
	values := balanceTestSettings(t)
	h := Handler{
		instanceStore:   balanceTestInstances{items: []storage.Instance{{ID: "node-1", Enabled: true}}},
		balanceSource:   balanceTestSource{users: map[string][]PassthroughUser{"node-1": {{ID: 7, Quota: 1, Status: 1}, {ID: 8, Quota: 1, Status: 0}}}},
		balanceUsage:    balanceTestUsage{rows: []storage.UserQuotaUsage{{InstanceID: "node-1", DimensionKey: "node-1:user:7", RequestCount: 9, Quota: 1000}, {InstanceID: "node-1", DimensionKey: "node-1:user:8", RequestCount: 100, Quota: 1000}}},
		balanceSettings: balanceTestSettingsStore{items: map[int64]storage.BalanceAlertUserSetting{7: {UserID: 7, Enabled: true}, 8: {UserID: 8, Enabled: true}}},
	}
	alerts, err := h.balanceAlerts(values, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 0 {
		t.Fatalf("expected no alerts, got %#v", alerts)
	}
}
