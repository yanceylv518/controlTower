package dashboard

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

type catalogStoreStub struct {
	filter billing.CatalogFilter
	called bool
	result billing.CatalogPage
}

func (s *catalogStoreStub) BillingCatalog(_ context.Context, f billing.CatalogFilter) (billing.CatalogPage, error) {
	s.called = true
	s.filter = f
	return s.result, nil
}

func TestBillingCatalogPermissionsAndValidation(t *testing.T) {
	for _, tc := range []struct {
		permission, kind string
		allowed          bool
	}{{"billing.users", "user_statement", true}, {"billing.users", "upstream_statement", false}, {"billing.channels", "upstream_statement", true}, {"billing.channels", "user_statement", false}, {"billing.tasks", "upstream_statement", true}} {
		s := &catalogStoreStub{}
		w := menuRequest(t, tc.permission, "GET", "/api/dashboard/billing/catalog?instance_id=site&kind="+tc.kind, BillingCatalogHandler{s})
		if s.called != tc.allowed || (!tc.allowed && w.Code != 403) {
			t.Fatalf("%+v: %d called=%v", tc, w.Code, s.called)
		}
	}
	for _, query := range []string{"kind=other&instance_id=s", "kind=user_statement", "kind=user_statement&instance_id=s&period=all", "kind=user_statement&instance_id=s&month=2026-13", "kind=user_statement&instance_id=s&page=0", "kind=user_statement&instance_id=s&page_size=101"} {
		s := &catalogStoreStub{}
		w := httptest.NewRecorder()
		BillingCatalogHandler{s}.ServeHTTP(w, httptest.NewRequest("GET", "/?"+query, nil))
		if w.Code != 400 || s.called {
			t.Fatal(query, w.Code, s.called)
		}
	}
}

func TestBillingCatalogFilterAndSavedCurrency(t *testing.T) {
	snapshot, err := billing.NewMoneySnapshot("site", `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting.quota_display_type":"CNY"}`, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s := &catalogStoreStub{result: billing.CatalogPage{Items: []billing.CatalogBill{{WorkspaceBill: billing.WorkspaceBill{Job: billing.Job{ID: "saved", MoneySnapshot: snapshot}, WorkspaceTotals: billing.WorkspaceTotals{Amount: "2", BeforeAmount: "", EmptyAmount: "0"}}}}, Total: 521, Page: 27, PageSize: 20}}
	w := httptest.NewRecorder()
	BillingCatalogHandler{s}.ServeHTTP(w, httptest.NewRequest("GET", "/?instance_id=site&kind=upstream_statement&period=daily&month=2026-09&q=%23%37&page=27&page_size=20", nil))
	var got billing.CatalogPage
	if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 {
		t.Fatal(w.Code, err, w.Body.String())
	}
	if s.filter.Query != "#7" || s.filter.Page != 27 || s.filter.Month != "2026-09" || got.Total != 521 || got.Items[0].Amount != "14.400000000000" || got.Items[0].BeforeAmount != "" || got.Items[0].Currency.Type != "CNY" {
		t.Fatal(s.filter, got)
	}
}
