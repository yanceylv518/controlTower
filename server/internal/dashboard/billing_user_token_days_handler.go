package dashboard

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"controltower/server/internal/billing"
)

type BillingUserTokenDaysStore interface {
	ListBillingUserTokenBillDays(context.Context, string, time.Time, time.Time, int64, int) ([]billing.UserTokenBillDay, error)
}

type BillingUserTokenDaysHandler struct{ Store BillingUserTokenDaysStore }

func (h BillingUserTokenDaysHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	site := strings.TrimSpace(q.Get("instance_id"))
	userID, userErr := strconv.ParseInt(strings.TrimSpace(q.Get("user_id")), 10, 64)
	from, fromErr := time.ParseInLocation("2006-01-02", strings.TrimSpace(q.Get("from")), billing.BusinessLocation)
	through, throughErr := time.ParseInLocation("2006-01-02", strings.TrimSpace(q.Get("through")), billing.BusinessLocation)
	today := time.Now().In(billing.BusinessLocation)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, billing.BusinessLocation)
	if site == "" || userErr != nil || userID <= 0 || fromErr != nil || throughErr != nil || through.Before(from) || !through.Before(today) || through.Sub(from) >= 366*24*time.Hour {
		writeDashboardError(w, http.StatusBadRequest, "invalid_date_range")
		return
	}
	if !billingSiteAllowed(r, site, userID) {
		writeDashboardError(w, http.StatusForbidden, "forbidden")
		return
	}
	if !billingReadonlyAvailable(h.Store, site) {
		writeDashboardError(w, http.StatusConflict, "readonly_source_unavailable")
		return
	}
	items, err := h.Store.ListBillingUserTokenBillDays(r.Context(), site, from, through.AddDate(0, 0, 1), userID, 100000)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_user_token_days_query_failed")
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"items": items})
}
