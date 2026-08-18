package dashboard

import (
	"context"
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

func TestBillingPricesPutValidatesAndAuditsVersionedTiers(t *testing.T) {
	store := &fakeBillingConfigStore{}
	handler := BillingPricesHandler{Store: store}
	body := `{"instance_id":"cn","model_name":"gpt-x","effective_from":"2026-08-03","tiers":[{"tier_from":0,"input_price":"2.10","output_price":"8.40","cache_price":"0.42"},{"tier_from":128000,"input_price":"4.20","output_price":"16.80","cache_price":"0.84"}]}`
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("PUT", "/api/dashboard/billing/prices", strings.NewReader(body)))
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(store.saved) != 2 || store.saved[1].TierFrom != 128000 {
		t.Fatalf("saved=%#v", store.saved)
	}
	if _, offset := store.saved[0].EffectiveFrom.Zone(); offset != 8*60*60 {
		t.Fatalf("effective date must use Shanghai business time: %s", store.saved[0].EffectiveFrom)
	}
	if len(store.audits) != 1 || store.audits[0].OperationType != "billing.price_update" {
		t.Fatalf("audits=%#v", store.audits)
	}
}

func TestBillingPricesPutRejectsScheduleWithoutZeroTier(t *testing.T) {
	store := &fakeBillingConfigStore{}
	handler := BillingPricesHandler{Store: store}
	body := `{"instance_id":"cn","model_name":"gpt-x","effective_from":"2026-08-03","tiers":[{"tier_from":1,"input_price":"2","output_price":"8","cache_price":"1"}]}`
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("PUT", "/", strings.NewReader(body)))
	if response.Code != 400 || len(store.saved) != 0 {
		t.Fatalf("status=%d saved=%#v", response.Code, store.saved)
	}
}

func TestBillingGroupRatioPutDefaultsLegacyActorAndAudits(t *testing.T) {
	store := &fakeBillingConfigStore{}
	handler := BillingGroupRatiosHandler{Store: store}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("PUT", "/", strings.NewReader(`{"instance_id":"cn","group_name":"vip","ratio":"1.2500"}`)))
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(store.ratios) != 1 || store.ratios[0].UpdatedBy != "legacy-admin" {
		t.Fatalf("ratios=%#v", store.ratios)
	}
	if len(store.audits) != 1 {
		t.Fatalf("audits=%#v", store.audits)
	}
}
