package dashboard

import (
	"controltower/server/internal/billing"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMonthlyPreviewSavedTotals(t *testing.T) {
	snapshot, _ := billing.NewMoneySnapshot("site", `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting.quota_display_type":"CNY"}`, time.Now())
	job := billing.Job{JobType: "user_statement", BillPeriod: "monthly", UsageVersion: 3, MoneySnapshot: snapshot}
	rows := []billing.StatementAggregateRow{{AggregateRow: billing.AggregateRow{ModelName: "model", RequestCount: 2, Amount: "1.25", BeforeAmount: "2.5", SettlementDiscount: "0.5"}}}
	for _, dimension := range []string{"month", "daily", "token"} {
		w := httptest.NewRecorder()
		BillingStatementResultHandler{Store: cnyWorkbookStore{}}.writeMonthlyPreview(w, httptest.NewRequest("GET", "/?dimension="+dimension, nil), job, rows)
		var v struct {
			Headers, Totals []string
			Rows            [][]string
			Total           int
			Currency        string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 {
			t.Fatal(w.Body.String(), err)
		}
		if v.Currency != "CNY" || v.Total != 1 || v.Totals[len(v.Totals)-1] != "9.000000" || v.Totals[len(v.Totals)-3] != "18.000000" {
			t.Fatalf("%s: %+v", dimension, v)
		}
		for _, h := range v.Headers {
			if h == "渠道" {
				t.Fatal("user channel exposed")
			}
		}
	}
	w := httptest.NewRecorder()
	BillingStatementResultHandler{Store: cnyWorkbookStore{}}.writeMonthlyPreview(w, httptest.NewRequest("GET", "/?dimension=invalid", nil), job, rows)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
