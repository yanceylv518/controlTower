package dashboard

import (
	"context"
	"controltower/server/internal/storage"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

type testSiteCurrencySource map[string]string

func (s testSiteCurrencySource) RatioSnapshotForBilling(_ context.Context, site string) (string, error) {
	if raw, ok := s[site]; ok {
		return raw, nil
	}
	return "", errors.New("unavailable")
}

type balanceCurrencyTestSource struct {
	balanceTestSource
	testSiteCurrencySource
}

func TestBalanceAlertFormatsUsingSiteCurrency(t *testing.T) {
	h := Handler{
		instanceStore:   balanceTestInstances{items: []storage.Instance{{ID: "node", SiteID: "cny", Enabled: true}}},
		balanceSource:   balanceCurrencyTestSource{balanceTestSource{users: map[string][]PassthroughUser{"cny": {{ID: 7, Username: "alice", Quota: 500000, Status: 1}}}}, testSiteCurrencySource{"cny": `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting.quota_display_type":"CNY"}`}},
		balanceUsage:    balanceTestUsage{rows: []storage.UserQuotaUsage{{InstanceID: "node", DimensionKey: "node:user:7", RequestCount: 30, Quota: 1500000}}},
		balanceSettings: balanceTestSettingsStore{items: map[int64]storage.BalanceAlertUserSetting{7: {UserID: 7, Enabled: true}}},
	}
	items, err := h.balanceAlerts(balanceTestSettings(t), time.Now().UTC())
	if err != nil || len(items) != 1 || !strings.Contains(items[0].Summary, "当前余额：¥7.20") {
		t.Fatalf("currency not applied: %+v %v", items, err)
	}
}

func TestCurrencyUsesEachNewAPISite(t *testing.T) {
	source := testSiteCurrencySource{
		"usd":    `{"QuotaPerUnit":"1000000","general_setting.quota_display_type":"USD"}`,
		"cny":    `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting.quota_display_type":"CNY"}`,
		"custom": `{"QuotaPerUnit":"500000","general_setting.quota_display_type":"CUSTOM","general_setting.custom_currency_symbol":"HK$","general_setting.custom_currency_exchange_rate":"7.8"}`,
		"tokens": `{"QuotaPerUnit":"500000","general_setting.quota_display_type":"TOKENS"}`,
	}
	for _, tc := range []struct {
		site, symbol string
		per          float64
	}{
		{"usd", "$", 1000000}, {"cny", "¥", 500000 / 7.2}, {"custom", "HK$", 500000 / 7.8}, {"tokens", "", 1},
	} {
		got, err := readSiteCurrency(context.Background(), source, tc.site)
		if err != nil || got.Symbol != tc.symbol || math.Abs(got.QuotaPerUnit-tc.per) > 0.001 {
			t.Fatalf("site %s: %+v %v", tc.site, got, err)
		}
	}
	if _, err := readSiteCurrency(context.Background(), source, "missing"); err == nil {
		t.Fatal("must not invent a currency when a site is unavailable")
	}
}
