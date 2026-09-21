package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type moneySourceStub struct {
	raw string
	err error
}

type failedMoneyStatementStore struct {
	statementStoreStub
	snapshot *billing.MoneySnapshot
}

func (s *failedMoneyStatementStore) FailedStatementMoneySnapshot(context.Context, string) (*billing.MoneySnapshot, error) {
	return s.snapshot, nil
}
func TestFailedStatementKeepsMoneyWhenOptionsUnavailable(t *testing.T) {
	snapshot, err := billing.NewMoneySnapshot("site-a", `{"QuotaPerUnit":"500000"}`, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	store := &failedMoneyStatementStore{snapshot: snapshot}
	h := BillingStatementsHandler{Store: store, Source: moneySourceStub{err: fmt.Errorf("offline")}}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/dashboard/billing/statements", strings.NewReader(`{"instance_id":"site-a","statement_type":"user","user_id":7,"from":"2026-09-01","to":"2026-09-02"}`)))
	if w.Code != 202 || store.job.MoneySnapshot == nil || store.job.MoneySnapshot.ID != snapshot.ID {
		t.Fatalf("code=%d job=%+v", w.Code, store.job)
	}
}

func (s moneySourceStub) RatioSnapshot(context.Context, string) (string, error) { return s.raw, s.err }
func TestStatementAutomaticallyCapturesMoney(t *testing.T) {
	for _, tc := range []struct {
		source moneySourceStub
		code   int
	}{
		{moneySourceStub{raw: `{"QuotaPerUnit":"500000","ct.quota_per_unit_source":"newapi_builtin_default_500000"}`}, 202},
		{moneySourceStub{err: fmt.Errorf("offline")}, 503},
		{moneySourceStub{raw: `{"QuotaPerUnit":"0"}`}, 503},
	} {
		store := &statementStoreStub{}
		h := BillingStatementsHandler{Store: store, Source: tc.source}
		r := httptest.NewRequest("POST", "/api/dashboard/billing/statements", strings.NewReader(`{"instance_id":"site-a","statement_type":"user","user_id":7,"from":"2026-09-01","to":"2026-09-02"}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		if tc.code == 202 {
			if store.job.MoneySnapshot == nil || store.job.MoneySnapshot.Validate("site-a") != nil {
				t.Fatal("snapshot missing")
			}
		} else if store.job.ID != "" {
			t.Fatal("failed observation still created job")
		}
	}
}
