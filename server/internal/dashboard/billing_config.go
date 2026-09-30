package dashboard

import (
	"context"
	ctauth "controltower/server/internal/auth"
	"encoding/json"
	"net/http"
)

type BillingSourceConfigStore interface {
	BillingDataSource(context.Context) (string, error)
	SetBillingDataSource(context.Context, string, string) error
}
type BillingConfigHandler struct{ Store BillingSourceConfigStore }

func (h BillingConfigHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !billingAdminAllowed(r) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	if r.Method == http.MethodPut {
		var v struct {
			DataSource string `json:"data_source"`
		}
		if json.NewDecoder(r.Body).Decode(&v) != nil || (v.DataSource != "source" && v.DataSource != "archive") {
			writeDashboardError(w, 400, "invalid_data_source")
			return
		}
		if h.Store.SetBillingDataSource(r.Context(), v.DataSource, ctauth.Actor(r)) != nil {
			writeDashboardError(w, 500, "billing_config_save_failed")
			return
		}
	} else if r.Method != http.MethodGet {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	source, err := h.Store.BillingDataSource(r.Context())
	if err != nil {
		writeDashboardError(w, 500, "billing_config_unavailable")
		return
	}
	writeDashboardJSON(w, 200, map[string]string{"data_source": source})
}
