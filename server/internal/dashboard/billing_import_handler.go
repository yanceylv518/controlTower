package dashboard

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"controltower/server/internal/billing"
)

type BillingRatioSource interface {
	RatioSnapshot(context.Context, string) (string, error)
}

type BillingImportPricesHandler struct{ Source BillingRatioSource }

type billingImportedPrice struct {
	ModelName     string `json:"model_name"`
	EffectiveFrom string `json:"effective_from"`
	Input         string `json:"input_price"`
	Output        string `json:"output_price"`
	Cache         string `json:"cache_price"`
	CacheWrite    string `json:"cache_write_price"`
}

func (h BillingImportPricesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !billingAdminAllowed(r) {
		writeDashboardError(w, http.StatusForbidden, "forbidden")
		return
	}
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if instanceID == "" || h.Source == nil {
		writeDashboardError(w, http.StatusBadRequest, "instance_id_required")
		return
	}
	raw, err := h.Source.RatioSnapshot(r.Context(), instanceID)
	if err != nil {
		writeDashboardError(w, http.StatusBadGateway, "newapi_ratio_query_failed")
		return
	}
	snapshot, err := billing.ParseRatioSnapshot(raw)
	if err != nil {
		writeDashboardError(w, http.StatusUnprocessableEntity, "newapi_ratio_invalid")
		return
	}
	models := make([]string, 0, len(snapshot.ModelRatio))
	for model := range snapshot.ModelRatio {
		models = append(models, model)
	}
	sort.Strings(models)
	items := make([]billingImportedPrice, 0, len(models))
	for _, model := range models {
		price, _, priceErr := billing.FallbackPrice(snapshot, model, "")
		if priceErr != nil {
			continue
		}
		items = append(items, billingImportedPrice{ModelName: model, EffectiveFrom: time.Now().In(billing.BusinessLocation).Format("2006-01-02"), Input: price.Input, Output: price.Output, Cache: price.Cache, CacheWrite: price.CacheWrite})
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"items": items, "instance_id": instanceID, "quota_per_unit": snapshot.QuotaPerUnit})
}

var _ BillingRatioSource = BillingReadonlySource{}
