package dashboard

import (
	"archive/zip"
	"bytes"
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewStatementEditionDoesNotReplaceLegacy(t *testing.T) {
	keys := map[bool]string{}
	for _, newEdition := range []bool{false, true} {
		body := `{"instance_id":"site","statement_type":"user","user_id":7,"from":"2025-09-01T10:15:00+08:00","to":"2025-09-01T10:16:00+08:00","exclude_zero_output":true`
		if newEdition {
			body += `,"new_edition":true`
		}
		body += `}`
		store := &statementStoreStub{}
		w := httptest.NewRecorder()
		BillingStatementsHandler{Store: store}.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(body)))
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		keys[newEdition] = store.job.RequestKey
		if newEdition {
			if store.job.UsageVersion != 3 || store.job.ExcludeZeroOutput || store.job.BillPeriod != "temporary" {
				t.Fatal(store.job)
			}
		} else if store.job.UsageVersion != 2 || !store.job.ExcludeZeroOutput {
			t.Fatal(store.job)
		}
	}
	if keys[false] == keys[true] {
		t.Fatal("old and new statements share identity")
	}
}
func TestBillingProjectionKeepsExactSettlementEvidence(t *testing.T) {
	for _, raw := range []string{`{"quota_before_discount":9007199254740993,"user_model_discount":0.85}`, `{"quota_before_discount":"9007199254740993","user_model_discount":"0.85"}`} {
		v := normalizeBillingLog(billing.PagedLogRecord{PromptTokens: sql.NullInt64{Int64: 1, Valid: true}}, raw)
		if v.QuotaBeforeDiscount != "9007199254740993" || v.UserModelDiscount != "0.85" {
			t.Fatalf("evidence lost: %+v", v)
		}
	}
	if v := parseBillingCacheUsage(`{"user_model_discount":"broken"}`); v.UserModelDiscount != "invalid" {
		t.Fatalf("invalid evidence silently erased: %+v", v)
	}
	for _, key := range []string{"quota_before_discount", "user_model_discount"} {
		if !strings.Contains(billingOtherProjection, key) {
			t.Fatal("source SQL omits", key)
		}
	}
}
func TestMonthlyStatementRejectsPartialRange(t *testing.T) {
	store := &statementStoreStub{}
	w := httptest.NewRecorder()
	BillingStatementsHandler{Store: store}.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"instance_id":"site","statement_type":"user","user_id":7,"from":"2025-09-02T00:00:00+08:00","to":"2025-10-01T00:00:00+08:00","new_edition":true,"period":"monthly"}`)))
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
}

type cnyWorkbookStore struct{ BillingStatementResultStore }

func (cnyWorkbookStore) QueryBillingTokenRows(context.Context, string, int64, int64, time.Time, time.Time) ([]billing.TokenDailyRow, error) {
	return []billing.TokenDailyRow{{MultimediaUsage: billing.MultimediaUsage{ImageInputTokens: 513, ImageOutputTokens: 7, AudioInputTokens: 31, AudioOutputTokens: 11}, Amount: "1.25", BeforeAmount: "2.5", SettlementDiscount: "0.5"}}, nil
}
func TestSettlementMonthlyExportConvertsSnapshotCNY(t *testing.T) {
	snapshot, err := billing.NewMoneySnapshot("site", `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting.quota_display_type":"CNY"}`, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	data, err := settlementWorkbook(billing.Job{JobType: "user_statement", BillPeriod: "monthly", UsageVersion: 3, MoneySnapshot: snapshot}, []billing.StatementAggregateRow{{AggregateRow: billing.AggregateRow{MultimediaUsage: billing.MultimediaUsage{ImageInputTokens: 513, ImageOutputTokens: 7, AudioInputTokens: 31, AudioOutputTokens: 11}, Amount: "1.25", BeforeAmount: "2.5", SettlementDiscount: "0.5"}}}, cnyWorkbookStore{})
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range z.File {
		if !strings.HasPrefix(f.Name, "xl/worksheets/sheet") {
			continue
		}
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		s := string(b)
		if strings.Contains(s, ">渠道<") {
			t.Fatal("user bill includes channel")
		}
		if strings.Index(s, "音频输出 Token") > strings.Index(s, "原价金额") {
			t.Fatal("usage columns follow prices")
		}
		if !strings.Contains(s, "折后金额 CNY") || !strings.Contains(s, "9.000000000000") || !strings.Contains(s, "18.000000000000") || !strings.Contains(s, "5 折") || !strings.Contains(s, "图像输入 Token") || !strings.Contains(s, "<v>513</v>") {
			t.Fatal("month sheet currency or amount mismatch", f.Name)
		}
	}
}
