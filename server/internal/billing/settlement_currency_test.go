package billing

import (
	"testing"
	"time"
)

func TestSettlementCurrencyFollowsSite(t *testing.T) {
	for _, v := range []struct{ raw, label, want string }{
		{`{"QuotaPerUnit":"500000","general_setting.quota_display_type":"USD"}`, "USD", "1.250000000000"},
		{`{"QuotaPerUnit":"500000","general_setting.quota_display_type":"CNY","USDExchangeRate":"7.2"}`, "CNY", "9.000000000000"},
		{`{"QuotaPerUnit":"500000","general_setting.quota_display_type":"CUSTOM","general_setting.custom_currency_symbol":"HK$","general_setting.custom_currency_exchange_rate":"7.8"}`, "HK$", "9.750000000000"},
		{`{"QuotaPerUnit":"500000","general_setting.quota_display_type":"TOKENS"}`, "额度", "625000.000000000000"},
	} {
		s, e := NewMoneySnapshot("site", v.raw, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		c, r, e := SettlementDisplay(s)
		if e != nil {
			t.Fatal(e)
		}
		if SettlementCurrencyLabel(c) != v.label || DisplaySettlementAmount("1.25", r) != v.want {
			t.Fatal(v.label, c)
		}
	}
}
