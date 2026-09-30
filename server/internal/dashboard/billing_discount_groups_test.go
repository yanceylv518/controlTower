package dashboard

import (
	"archive/zip"
	"bytes"
	"context"
	"controltower/server/internal/billing"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http/httptest"
	"testing"
	"time"
)

type discountGroupsStore struct {
	BillingStatementResultStore
	tokens []billing.TokenDailyRow
}

func (s discountGroupsStore) QueryBillingTokenRows(context.Context, string, int64, int64, time.Time, time.Time) ([]billing.TokenDailyRow, error) {
	return s.tokens, nil
}

func discountGroupFixture() (billing.Job, []billing.StatementAggregateRow, discountGroupsStore) {
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	snapshot, _ := billing.NewMoneySnapshot("site", `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting.quota_display_type":"CNY"}`, time.Now())
	job := billing.Job{JobType: "user_statement", BillPeriod: "monthly", UsageVersion: 3, MoneySnapshot: snapshot, From: day, To: day.AddDate(0, 1, 0)}
	rows := []billing.StatementAggregateRow{}
	store := discountGroupsStore{}
	for i, v := range []struct{ discount, before, amount string }{{"1", "1", "1"}, {"0.46", "1", "0.46"}, {"0.460000", "2", "0.92"}, {"0", "3", "0"}} {
		row := billing.StatementAggregateRow{AggregateRow: billing.AggregateRow{Day: day.AddDate(0, 0, i/2), ModelName: "glm-5.3-flash", RequestCount: 1, PromptTokens: 10, BeforeAmount: v.before, Amount: v.amount, SettlementDiscount: v.discount}, ChannelID: 9}
		rows = append(rows, row)
		store.tokens = append(store.tokens, billing.TokenDailyRow{Day: row.Day, ModelName: row.ModelName, TokenName: "prod", RequestCount: row.RequestCount, PromptTokens: row.PromptTokens, BeforeAmount: v.before, Amount: v.amount, SettlementDiscount: v.discount})
	}
	return job, rows, store
}

func TestMonthlyDiscountGroupsPreviewAndWorkbook(t *testing.T) {
	job, rows, store := discountGroupFixture()
	for _, dimension := range []string{"month", "daily", "token"} {
		w := httptest.NewRecorder()
		BillingStatementResultHandler{Store: store}.writeMonthlyPreview(w, httptest.NewRequest("GET", "/?dimension="+dimension, nil), job, rows)
		var v struct {
			Headers, Totals []string
			Rows            [][]string
			Legacy          bool `json:"legacy_mixed_discounts"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 {
			t.Fatal(w.Body.String(), err)
		}
		wantRows := 4
		if dimension == "month" {
			wantRows = 3
		}
		if len(v.Rows) != wantRows || v.Legacy || v.Totals[len(v.Headers)-1] != "17.136000" || v.Totals[len(v.Headers)-3] != "50.400000" {
			t.Fatal(dimension, v)
		}
		labels := map[string]int{}
		for _, row := range v.Rows {
			labels[row[len(v.Headers)-2]]++
		}
		if labels["原价"] != 1 || labels["0 折"] != 1 || labels["4.6 折"] != wantRows-2 {
			t.Fatal(dimension, labels)
		}
	}
	book, err := settlementWorkbook(job, rows, store)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(book), int64(len(book)))
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml", "xl/worksheets/sheet3.xml"} {
		f, err := z.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		var sheet struct {
			Rows []struct {
				Cells []struct {
					Value string `xml:"v"`
					Text  string `xml:"is>t"`
				} `xml:"c"`
			} `xml:"sheetData>row"`
		}
		if err = xml.Unmarshal(data, &sheet); err != nil {
			t.Fatal(err)
		}
		labels := map[string]int{}
		for _, row := range sheet.Rows {
			if len(row.Cells) < 14 {
				continue
			}
			label := row.Cells[len(row.Cells)-2].Text
			if label == "原价" || label == "4.6 折" || label == "0 折" {
				labels[label]++
			}
		}
		wantDiscountRows := 2
		if i == 0 {
			wantDiscountRows = 1
		}
		if labels["原价"] != 1 || labels["4.6 折"] != wantDiscountRows || labels["0 折"] != 1 {
			t.Fatal(name, labels)
		}
	}
}

func TestHistoricalMixedDiscountRemainsExplicitlyUnsplit(t *testing.T) {
	job, rows, store := discountGroupFixture()
	rows = rows[:1]
	rows[0].SettlementDiscount, rows[0].BeforeAmount = "mixed", ""
	w := httptest.NewRecorder()
	BillingStatementResultHandler{Store: store}.writeMonthlyPreview(w, httptest.NewRequest("GET", "/?dimension=month", nil), job, rows)
	var v struct {
		Headers, Totals []string
		Rows            [][]string
		Legacy          bool `json:"legacy_mixed_discounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(v.Rows) != 1 || !v.Legacy || v.Rows[0][len(v.Headers)-2] != "旧账单未拆分" || v.Totals[len(v.Headers)-3] != "—" || v.Totals[len(v.Headers)-1] != "7.200000" {
		t.Fatal(v)
	}
}

func TestSettlementDiscountGroupsPreserveChannelsDatesAndPrecision(t *testing.T) {
	job, rows, _ := discountGroupFixture()
	rows[2].ChannelID = 10
	for _, tc := range []struct {
		kind  string
		daily bool
		count int
	}{
		{"user_statement", false, 3}, {"user_statement", true, 4}, {"upstream_statement", false, 4}, {"upstream_statement", true, 4},
	} {
		job.JobType = tc.kind
		grouped := groupStatementRows(job, rows, nil, tc.daily)
		if len(grouped) != tc.count {
			t.Fatal(tc, grouped)
		}
	}
	job.JobType = "user_statement"
	rows[1].Amount, rows[2].Amount = "0.000000000001", "0.000000000002"
	for _, group := range groupStatementRows(job, rows, nil, false) {
		if group.Row.SettlementDiscount == "0.460000" && group.Row.Amount != "0.000000000003" {
			t.Fatal("saved amount precision lost", group)
		}
	}
}
