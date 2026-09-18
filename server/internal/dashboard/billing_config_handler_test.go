package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"controltower/server/internal/billing"
	"controltower/server/internal/storage"
)

type fakeBillingConfigStore struct {
	prices []billing.PriceRecord
	saved  []billing.PriceRecord
	ratios []billing.GroupRatio
	audits []storage.OperationAudit
}

func (f *fakeBillingConfigStore) ListBillingPrices(context.Context, string) ([]billing.PriceRecord, error) {
	return f.prices, nil
}
func (f *fakeBillingConfigStore) PutBillingPriceSchedule(_ context.Context, v []billing.PriceRecord) error {
	f.saved = append([]billing.PriceRecord(nil), v...)
	return nil
}
func (f *fakeBillingConfigStore) ListBillingGroupRatios(context.Context, string) ([]billing.GroupRatio, error) {
	return f.ratios, nil
}
func (f *fakeBillingConfigStore) PutBillingGroupRatio(_ context.Context, v billing.GroupRatio) error {
	f.ratios = append(f.ratios, v)
	return nil
}
func (f *fakeBillingConfigStore) InsertOperationAudit(v storage.OperationAudit) error {
	f.audits = append(f.audits, v)
	return nil
}

func TestRetiredBillingWritesDoNotMutate(t *testing.T) {
	store := &fakeBillingConfigStore{}
	for _, handler := range []http.Handler{BillingPricesHandler{Store: store}, BillingGroupRatiosHandler{Store: store}} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("PUT", "/", strings.NewReader(`{"instance_id":"cn","group_name":"vip","ratio":"1.25"}`)))
		if response.Code != 405 {
			t.Fatalf("status=%d", response.Code)
		}
	}
	if len(store.saved)+len(store.ratios)+len(store.audits) != 0 {
		t.Fatal("retired writes mutated data")
	}
}
