package dashboard

import (
	"controltower/server/internal/billing"
	"encoding/json"
	"net/http/httptest"
	"strings"
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

func TestMonthlyPreviewDistinctUnitPrices(t *testing.T) {
	snapshot, _ := billing.NewMoneySnapshot("site", `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting.quota_display_type":"CNY"}`, time.Now())
	job := billing.Job{JobType: "user_statement", BillPeriod: "monthly", UsageVersion: 3, MoneySnapshot: snapshot}
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	rows := []billing.StatementAggregateRow{}
	for i, price := range []string{"2", "3", "2"} {
		rows = append(rows, billing.StatementAggregateRow{AggregateRow: billing.AggregateRow{Day: day.AddDate(0, 0, i), ModelName: "m", RequestCount: 1, Amount: "1", BeforeAmount: "1", UnitPrices: billing.UnitPrices{"输入|" + price: true, "输出|8": true}}})
	}
	for _, dimension := range []string{"month", "daily"} {
		w := httptest.NewRecorder()
		BillingStatementResultHandler{Store: cnyWorkbookStore{}}.writeMonthlyPreview(w, httptest.NewRequest("GET", "/?dimension="+dimension, nil), job, rows)
		var v struct {
			Headers, Totals []string
			Rows            [][]string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 {
			t.Fatal(w.Body.String(), err)
		}
		col := len(v.Headers) - 4
		if !strings.HasPrefix(v.Headers[col], "模型单价（CNY") || v.Totals[col] != "" || v.Totals[len(v.Totals)-1] != "21.600000" {
			t.Fatal(v)
		}
		if dimension == "month" && (len(v.Rows) != 1 || v.Rows[0][col] != "输入 14.4 / 21.6；输出 57.6") {
			t.Fatal(v.Rows)
		}
		if dimension == "daily" && (len(v.Rows) != 3 || v.Rows[0][col] != "输入 14.4；输出 57.6" || v.Rows[1][col] != "输入 21.6；输出 57.6") {
			t.Fatal(v.Rows)
		}
	}
}
