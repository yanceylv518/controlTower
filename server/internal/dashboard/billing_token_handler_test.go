package dashboard

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/billing"
	"controltower/server/internal/storage"
)

type tokenHandlerStore struct {
	fakeBillingSummaryStore
	tokenRows []billing.TokenDailyRow
}

func (s tokenHandlerStore) QueryBillingTokenRows(context.Context, string, int64, int64) ([]billing.TokenDailyRow, error) {
	return s.tokenRows, nil
}

func TestBillingTokensMarksOldJobDataMissing(t *testing.T) {
	store := tokenHandlerStore{fakeBillingSummaryStore: fakeBillingSummaryStore{rows: []billing.AggregateRow{{UserID: 7, RequestCount: 1}}}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/dashboard/billing/tokens?instance_id=site-a&user_id=7&month=2026-08", nil)
	BillingTokenHandler{Store: store}.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"token_data_missing":true`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
func TestBillingTokensUsesBillingRead409(t *testing.T) {
	store := tokenHandlerStore{fakeBillingSummaryStore: fakeBillingSummaryStore{missingJob: true}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/dashboard/billing/tokens?instance_id=site-a&user_id=7&month=2026-08", nil)
	BillingTokenHandler{Store: store}.ServeHTTP(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "billing_not_generated") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
func TestTokenCSVHasTwoSectionsAndBOM(t *testing.T) {
	w := httptest.NewRecorder()
	writeTokenCSV(w, 7, 3, time.Now(), time.Now().Add(time.Hour), []billing.TokenSummary{{TokenID: 3, TokenName: "key"}}, []billing.DetailItem{{Day: "2026-08-01", ModelName: "m"}})
	body := w.Body.String()
	if !strings.HasPrefix(body, "\xef\xbb\xbf") || !strings.Contains(body, "令牌汇总") || !strings.Contains(body, "令牌日账单") {
		t.Fatalf("csv=%q", body)
	}
}

func TestTokenBillingScopeMatrix(t *testing.T) {
	cases := []struct {
		name string
		user storage.User
		site string
		uid  int64
		want bool
	}{{"admin", storage.User{Role: "admin"}, "other", 99, true}, {"viewer allowed", storage.User{Role: "viewer", ScopeSite: "site-a", ScopeUserIDs: []int64{7}}, "site-a", 7, true}, {"wrong site", storage.User{Role: "viewer", ScopeSite: "site-a", ScopeUserIDs: []int64{7}}, "site-b", 7, false}, {"wrong user", storage.User{Role: "viewer", ScopeSite: "site-a", ScopeUserIDs: []int64{7}}, "site-a", 8, false}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tokenBillingScopeAllowed(tc.user, tc.site, tc.uid); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
