package dashboard

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChannelFullPriceRuleRejected(t *testing.T) {
	for _, method := range []string{"POST", "PUT"} {
		for _, value := range []string{"1", "1.000000"} {
			w := httptest.NewRecorder()
			body := `{"id":4,"instance_id":"test","discount_type":"upstream_channel","subject_id":2,"channel_id":5,"discount":"` + value + `","effective_from":"2026-07-01T00:00:00+08:00"}`
			(BillingDiscountHandler{}).ServeHTTP(w, httptest.NewRequest(method, "/", strings.NewReader(body)))
			if w.Code != 400 || !strings.Contains(w.Body.String(), "channel_discount_full_price") {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
}
