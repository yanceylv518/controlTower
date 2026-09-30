package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

type workspaceModelStore struct{ rows []billing.WorkspaceBill }

func (s workspaceModelStore) BillingWorkspace(context.Context, string, string, int64, time.Time, time.Time) ([]billing.WorkspaceBill, error) {
	return s.rows, nil
}

func TestWorkspaceModelAmountsUseBillCurrencySnapshot(t *testing.T) {
	snapshot, err := billing.NewMoneySnapshot("site", `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting.quota_display_type":"CNY"}`, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	models := []billing.WorkspaceModel{
		{Model: "a", WorkspaceTotals: billing.WorkspaceTotals{Requests: 1, Amount: "1.25", BeforeAmount: "2.5", EmptyAmount: "0.25", Discount: "0.5"}},
		{Model: "b", WorkspaceTotals: billing.WorkspaceTotals{Requests: 1, Amount: "0.25", BeforeAmount: "", EmptyAmount: "0", Discount: "0.46"}},
	}
	store := workspaceModelStore{rows: []billing.WorkspaceBill{{Job: billing.Job{ID: "day", MoneySnapshot: snapshot}, Models: models, WorkspaceTotals: billing.SumWorkspaceModels(models)}}}
	w := httptest.NewRecorder()
	BillingWorkspaceHandler{Store: store}.ServeHTTP(w, httptest.NewRequest("GET", "/?instance_id=site&kind=user_statement&subject_id=7&from=2026-09-01&to=2026-10-01", nil))
	var result struct {
		Items []billing.WorkspaceBill `json:"items"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	if len(result.Items) != 1 || len(result.Items[0].Models) != 2 {
		t.Fatal(result)
	}
	bill := result.Items[0]
	if bill.Currency.Type != "CNY" || bill.Amount != "10.800000000000" || bill.BeforeAmount != "" || bill.EmptyAmount != "1.800000000000" {
		t.Fatal(bill)
	}
	if bill.Models[0].Amount != "9.000000000000" || bill.Models[0].BeforeAmount != "18.000000000000" || bill.Models[0].EmptyAmount != "1.800000000000" || bill.Models[0].Discount != "0.5" || bill.Models[1].Amount != "1.800000000000" || bill.Models[1].BeforeAmount != "" {
		t.Fatal(bill.Models)
	}
}
