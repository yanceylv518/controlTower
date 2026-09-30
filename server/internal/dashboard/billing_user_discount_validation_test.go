package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUserSupplementCurrentDiscountGuard(t *testing.T) {
	for _, tc := range []struct {
		name       string
		discounted bool
		err        error
		want       int
	}{{"discount", true, nil, 400}, {"original", false, nil, 200}, {"unavailable", false, errors.New("offline"), 502}} {
		t.Run(tc.name, func(t *testing.T) {
			store := &auditDiscountStore{items: []billing.DiscountRule{{ID: 1}}}
			h := BillingDiscountHandler{Store: store, CurrentDiscount: func(context.Context, string, int64, string) (bool, error) { return tc.discounted, tc.err }}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("PUT", "/", strings.NewReader(`{"id":1,"instance_id":"test","discount_type":"user_model","subject_id":2,"model_name":"model","discount":"0.8","effective_from":"2020-01-01T00:00:00Z"}`)))
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
			w = httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("PUT", "/", strings.NewReader(`{"id":1,"instance_id":"test","discount_type":"user_model","subject_id":2,"model_name":"model","discount":"0.8","effective_from":"2020-01-01T00:00:00Z","effective_to":"2020-02-01T00:00:00Z"}`)))
			if w.Code != 200 {
				t.Fatal("historical supplement", w.Code, w.Body.String())
			}
		})
	}
}
