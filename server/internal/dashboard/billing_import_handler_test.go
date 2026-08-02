package dashboard

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeBillingRatioSource struct {
	raw string
	err error
}

func (f fakeBillingRatioSource) RatioSnapshot(context.Context, string) (string, error) {
	return f.raw, f.err
}

func TestBillingImportPricesConvertsNewAPIRatios(t *testing.T) {
	raw := `{"ModelRatio":"{\"model-b\":2,\"model-a\":1}","CompletionRatio":"{\"model-a\":4}","CacheRatio":"{\"model-a\":0.5}","GroupRatio":"{}","QuotaPerUnit":500000}`
	req := httptest.NewRequest("GET", "/api/dashboard/billing/import-prices?instance_id=site-a", nil)
	w := httptest.NewRecorder()
	BillingImportPricesHandler{Source: fakeBillingRatioSource{raw: raw}}.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"model_name":"model-a"`) || !strings.Contains(w.Body.String(), `"input_price":"2.000000000000"`) || !strings.Contains(w.Body.String(), `"output_price":"8.000000000000"`) || !strings.Contains(w.Body.String(), `"cache_price":"1.000000000000"`) {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
}

func TestBillingImportPricesRejectsInvalidSnapshot(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/dashboard/billing/import-prices?instance_id=site-a", nil)
	w := httptest.NewRecorder()
	BillingImportPricesHandler{Source: fakeBillingRatioSource{raw: `{}`}}.ServeHTTP(w, req)
	if w.Code != 422 {
		t.Fatalf("got %d want 422", w.Code)
	}
}
