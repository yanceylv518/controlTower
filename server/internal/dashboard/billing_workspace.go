package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type BillingWorkspaceStore interface {
	BillingWorkspace(context.Context, string, string, int64, time.Time, time.Time) ([]billing.WorkspaceBill, error)
}
type BillingWorkspaceHandler struct{ Store BillingWorkspaceStore }

func (h BillingWorkspaceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !billingAdminAllowed(r) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	q := r.URL.Query()
	site, kind := strings.TrimSpace(q.Get("instance_id")), q.Get("kind")
	id, err := strconv.ParseInt(q.Get("subject_id"), 10, 64)
	from, to, e := parseBillingInputRange(q.Get("from"), q.Get("to"))
	if err != nil || e != nil || site == "" || id <= 0 || (kind != "user_statement" && kind != "upstream_statement") || to.Sub(from) > 93*24*time.Hour {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	if !billingTypeAllowed(r, kind) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	rows, err := h.Store.BillingWorkspace(r.Context(), site, kind, id, from, to)
	if err != nil {
		writeDashboardError(w, 500, "billing_workspace_failed")
		return
	}
	for i := range rows {
		display, rate, e := billing.SettlementDisplay(rows[i].Job.MoneySnapshot)
		if e != nil {
			writeDashboardError(w, 500, "billing_currency_unavailable")
			return
		}
		rows[i].Currency = display
		rows[i].Amount = billing.DisplaySettlementAmount(rows[i].Amount, rate)
		rows[i].BeforeAmount = billing.DisplaySettlementAmount(rows[i].BeforeAmount, rate)
		rows[i].EmptyAmount = billing.DisplaySettlementAmount(rows[i].EmptyAmount, rate)
	}
	remaining := 0
	if store, ok := h.Store.(interface {
		BillingRemainingDays(context.Context, string, string, int64, time.Time, time.Time) (int, error)
	}); ok {
		remaining, err = store.BillingRemainingDays(r.Context(), site, kind, id, from, to)
		if err != nil {
			writeDashboardError(w, 500, "billing_progress_failed")
			return
		}
	}
	writeDashboardJSON(w, 200, map[string]any{"items": rows, "remaining_days": remaining})
}
