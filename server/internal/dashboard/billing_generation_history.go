package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"net/http"
	"strconv"
)

type billingTaskHistoryStore interface {
	ListBillingGenerationTasks(context.Context, string, string, int, int) ([]billing.GenerationTask, int, error)
	BillingGenerationTask(context.Context, string, string) (billing.GenerationTask, error)
}

func (h BillingBatchGenerationHandler) serveTaskHistory(w http.ResponseWriter, r *http.Request, site, kind string) {
	if site == "" || !billingSiteAllowed(r, site, 0) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	store, ok := h.Store.(billingTaskHistoryStore)
	if !ok {
		writeDashboardError(w, 500, "billing_history_unavailable")
		return
	}
	allowed := func(t billing.GenerationTask) bool {
		for _, id := range t.SubjectIDs {
			if !billingBatchSubjectAllowed(r, site, t.Kind, id) {
				return false
			}
		}
		return t.Kind == kind && billingTypeAllowed(r, t.Kind)
	}
	if r.URL.Query().Get("action") == "history-detail" {
		t, e := store.BillingGenerationTask(r.Context(), site, r.URL.Query().Get("id"))
		if e == sql.ErrNoRows {
			writeDashboardError(w, 404, "job_not_found")
			return
		}
		if e != nil {
			writeDashboardError(w, 500, "billing_history_unavailable")
			return
		}
		if !allowed(t) {
			writeDashboardError(w, 403, "forbidden")
			return
		}
		writeDashboardJSON(w, 200, t)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	items, total, e := store.ListBillingGenerationTasks(r.Context(), site, kind, 20, (page-1)*20)
	if e != nil {
		writeDashboardError(w, 500, "billing_history_unavailable")
		return
	}
	visible := []billing.GenerationTask{}
	for _, t := range items {
		if allowed(t) {
			t.Items = nil
			visible = append(visible, t)
		}
	}
	writeDashboardJSON(w, 200, map[string]any{"items": visible, "total": total, "page": page, "page_size": 20})
}
