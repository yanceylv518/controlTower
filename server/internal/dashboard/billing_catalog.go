package dashboard

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"controltower/server/internal/billing"
)

type BillingCatalogStore interface {
	BillingCatalog(context.Context, billing.CatalogFilter) (billing.CatalogPage, error)
}
type BillingCatalogHandler struct{ Store BillingCatalogStore }

func (h BillingCatalogHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !billingAdminAllowed(r) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	q := r.URL.Query()
	f := billing.CatalogFilter{Site: strings.TrimSpace(q.Get("instance_id")), Kind: q.Get("kind"), Period: q.Get("period"), Month: q.Get("month"), Query: strings.TrimSpace(q.Get("q")), Page: 1, PageSize: 20}
	if f.Period == "" {
		f.Period = "monthly"
	}
	if f.Site == "" || (f.Kind != "user_statement" && f.Kind != "upstream_statement") || (f.Period != "daily" && f.Period != "monthly" && f.Period != "temporary") || len(f.Query) > 1024 {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	if !billingTypeAllowed(r, f.Kind) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	if f.Month != "" {
		if v, e := time.Parse("2006-01", f.Month); e != nil || v.Year() < 1000 || v.Year() > 9998 {
			writeDashboardError(w, 400, "invalid_query")
			return
		}
	}
	for key, dest := range map[string]*int{"page": &f.Page, "page_size": &f.PageSize} {
		if q.Has(key) {
			n, e := strconv.Atoi(q.Get(key))
			if e != nil || n < 1 || (key == "page_size" && n > 100) || n > 1000000 {
				writeDashboardError(w, 400, "invalid_query")
				return
			}
			*dest = n
		}
	}
	page, err := h.Store.BillingCatalog(r.Context(), f)
	if err != nil {
		writeDashboardError(w, 500, "billing_catalog_failed")
		return
	}
	for i := range page.Items {
		v := &page.Items[i]
		display, rate, e := billing.SettlementDisplay(v.Job.MoneySnapshot)
		if e != nil {
			writeDashboardError(w, 500, "billing_currency_unavailable")
			return
		}
		v.Currency = display
		v.Amount = billing.DisplaySettlementAmount(v.Amount, rate)
		v.BeforeAmount = billing.DisplaySettlementAmount(v.BeforeAmount, rate)
		v.EmptyAmount = billing.DisplaySettlementAmount(v.EmptyAmount, rate)
	}
	writeDashboardJSON(w, 200, page)
}
