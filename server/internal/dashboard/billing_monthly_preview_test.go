package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"encoding/json"
	"fmt"
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

type monthlyTierStore struct {
	cnyWorkbookStore
	saved []billing.DailyTierStatistics
	calls int
}

func (s *monthlyTierStore) QueryBillingDailyTierStatistics(_ context.Context, id string) ([]billing.DailyTierStatistics, error) {
	s.calls++
	if id != "frozen-month" {
		return nil, fmt.Errorf("wrong snapshot")
	}
	return s.saved, nil
}
func TestMonthlyPreviewDailyTiersFrozenAndScoped(t *testing.T) {
	snapshot, _ := billing.NewMoneySnapshot("site", `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting.quota_display_type":"CNY"}`, time.Now())
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	job := billing.Job{ID: "frozen-month", JobType: "user_statement", BillPeriod: "monthly", UsageVersion: 3, MoneySnapshot: snapshot, From: day, To: day.AddDate(0, 1, 0)}
	store := &monthlyTierStore{}
	rows := []billing.StatementAggregateRow{}
	// 51 parent groups exercise page-relative child indices and parent-only totals.
	for i := 0; i < 51; i++ {
		model := fmt.Sprintf("m%02d", i)
		rows = append(rows, billing.StatementAggregateRow{AggregateRow: billing.AggregateRow{Day: day, ModelName: model, RequestCount: 2, Amount: "1.25", BeforeAmount: "2.5", SettlementDiscount: "0.5"}})
		store.saved = append(store.saved, billing.DailyTierStatistics{Day: "2026-09-01", TierStatistics: billing.TierStatistics{Model: model, Tier: "高峰", Discount: "0.5", Requests: 1, Amount: "0.75", BeforeAmount: "1.5"}})
	}
	for _, v := range []billing.DailyTierStatistics{
		{Day: "2026-09-01", TierStatistics: billing.TierStatistics{Model: "m50", Tier: "低谷", Discount: "0.5", Requests: 1, Amount: "0.5", BeforeAmount: "1"}},
		{Day: "2026-09-02", TierStatistics: billing.TierStatistics{Model: "m50", Tier: "高峰", Discount: "0.5", Requests: 99, Amount: "99"}},
		{Day: "2026-09-01", TierStatistics: billing.TierStatistics{Model: "m50", Tier: "高峰", Discount: "0.8", Requests: 99, Amount: "99"}},
		{Day: "2026-09-01", TierStatistics: billing.TierStatistics{Model: "m50", Tier: "普通", Discount: "0.5", Requests: 99, Amount: "99"}},
	} {
		store.saved = append(store.saved, v)
	}
	w := httptest.NewRecorder()
	BillingStatementResultHandler{Store: store}.writeMonthlyPreview(w, httptest.NewRequest("GET", "/?dimension=daily&page=2", nil), job, rows)
	var result struct {
		Rows     [][]string
		TierRows map[int][][]string `json:"tier_rows"`
		Totals   []string
		Total    int
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 {
		t.Fatal(w.Body.String(), err)
	}
	if len(result.Rows) != 1 || result.Total != 51 || len(result.TierRows) != 1 || len(result.TierRows[0]) != 2 {
		t.Fatalf("bad pagination or scope: %+v", result)
	}
	children := result.TierRows[0]
	if children[0][1] != "高峰" || children[0][len(children[0])-1] != "5.400000000000" || children[1][1] != "低谷" || children[1][len(children[1])-1] != "3.600000000000" {
		t.Fatal(children)
	}
	if result.Totals[2] != "102" || result.Totals[len(result.Totals)-1] != "459.000000" {
		t.Fatal("children double counted", result.Totals)
	}
	for _, dim := range []string{"month", "token"} {
		w = httptest.NewRecorder()
		BillingStatementResultHandler{Store: store}.writeMonthlyPreview(w, httptest.NewRequest("GET", "/?dimension="+dim, nil), job, rows)
		if w.Code != 200 || store.calls != 1 {
			t.Fatal("unexpected tier lookup", dim, w.Code, store.calls)
		}
	}
	store.saved = nil
	w = httptest.NewRecorder()
	BillingStatementResultHandler{Store: store}.writeMonthlyPreview(w, httptest.NewRequest("GET", "/?dimension=daily", nil), job, rows)
	result.TierRows = nil
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.TierRows) != 0 || len(result.Rows) != 50 {
		t.Fatal("old bill must not invent tiers", w.Body.String(), err)
	}
}
