package dashboard

import (
	"context"
	"net/http"
	"strings"
	"time"

	"controltower/server/internal/billing"
)

type BillingOverviewStore interface {
	ListBillingDailyOverview(context.Context, string, time.Time, time.Time, int) ([]billing.DailyOverview, error)
}

type BillingUserDaysStore interface {
	ListBillingUserBillDays(context.Context, string, time.Time, time.Time, int64, string, int) ([]billing.UserBillDay, error)
}

type BillingOverviewHandler struct{ Store BillingOverviewStore }

func (h BillingOverviewHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	site := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if site != "" && !billingSiteAllowed(r, site, 0) {
		writeDashboardError(w, http.StatusForbidden, "forbidden")
		return
	}
	to := time.Now().In(billing.BusinessLocation).AddDate(0, 0, 1)
	from := to.AddDate(0, 0, -90)
	if raw := strings.TrimSpace(r.URL.Query().Get("month")); raw != "" {
		month, err := time.ParseInLocation("2006-01", raw, billing.BusinessLocation)
		if err != nil {
			writeDashboardError(w, http.StatusBadRequest, "invalid_month")
			return
		}
		from, to = month, month.AddDate(0, 1, 0)
	}
	items, err := h.Store.ListBillingDailyOverview(r.Context(), site, from, to, 500)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_overview_query_failed")
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"items": items, "from": from, "to": to})
}

type BillingUserDaysHandler struct{ Store BillingUserDaysStore }

func (h BillingUserDaysHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	site := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if site == "" {
		writeDashboardError(w, http.StatusBadRequest, "invalid_instance_id")
		return
	}
	if !billingSiteAllowed(r, site, 0) {
		writeDashboardError(w, http.StatusForbidden, "forbidden")
		return
	}
	from := time.Date(2000, 1, 1, 0, 0, 0, 0, billing.BusinessLocation)
	to := time.Now().In(billing.BusinessLocation).AddDate(0, 0, 1)
	period := ""
	if raw := strings.TrimSpace(r.URL.Query().Get("month")); raw != "" {
		parsed, err := time.ParseInLocation("2006-01", raw, billing.BusinessLocation)
		if err != nil {
			writeDashboardError(w, http.StatusBadRequest, "invalid_month")
			return
		}
		from, to, period = parsed, parsed.AddDate(0, 1, 0), parsed.Format("2006-01")
	}
	items, err := h.Store.ListBillingUserBillDays(r.Context(), site, from, to, int64(positiveInt(r.URL.Query().Get("user_id"), 0)), strings.TrimSpace(r.URL.Query().Get("search")), 5000)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_user_days_query_failed")
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"items": items, "month": period})
}
