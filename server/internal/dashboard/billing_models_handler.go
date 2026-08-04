package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
	"controltower/server/internal/storage"
)

type BillingModelStore interface {
	ListBillingPrices(context.Context, string) ([]billing.PriceRecord, error)
	ListBillingModelMetadata(context.Context, string) ([]billing.ModelMetadata, error)
	PutBillingModelMetadata(context.Context, billing.ModelMetadata) error
	UpsertBillingModels(context.Context, string, []string, time.Time, string) error
	PutBillingPriceSchedule(context.Context, []billing.PriceRecord) error
	InsertOperationAudit(storage.OperationAudit) error
}

type BillingModelsHandler struct {
	Store  BillingModelStore
	Source BillingModelSource
}

type BillingModelSource interface {
	BillingRatioSource
	ConfiguredModels(context.Context, string) ([]string, error)
}

type billingModelItem struct {
	ModelName        string `json:"model_name"`
	MaxContextTokens int64  `json:"max_context_tokens"`
	Input            string `json:"input_price"`
	Output           string `json:"output_price"`
	Cache            string `json:"cache_price"`
	EffectiveFrom    string `json:"effective_from"`
	PriceSource      string `json:"price_source"`
}

func (h BillingModelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !billingAdminAllowed(r) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.list(w, r)
	case http.MethodPost:
		h.sync(w, r)
	case http.MethodPut:
		var req struct {
			InstanceID       string `json:"instance_id"`
			ModelName        string `json:"model_name"`
			MaxContextTokens int64  `json:"max_context_tokens"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || strings.TrimSpace(req.InstanceID) == "" || strings.TrimSpace(req.ModelName) == "" || req.MaxContextTokens < 0 {
			writeDashboardError(w, 400, "invalid_model_metadata")
			return
		}
		actor := ctauth.Actor(r)
		if actor == "" {
			actor = "legacy-admin"
		}
		v := billing.ModelMetadata{InstanceID: strings.TrimSpace(req.InstanceID), ModelName: strings.TrimSpace(req.ModelName), MaxContextTokens: req.MaxContextTokens, UpdatedAt: time.Now().UTC(), UpdatedBy: actor}
		if err := h.Store.PutBillingModelMetadata(r.Context(), v); err != nil {
			writeDashboardError(w, 500, "update_failed")
			return
		}
		billingConfigAudit(h.Store, r, v.InstanceID, "billing.model_metadata_update", v.ModelName, v)
		writeDashboardJSON(w, 200, v)
	default:
		writeDashboardError(w, 405, "method_not_allowed")
	}
}

func (h BillingModelsHandler) list(w http.ResponseWriter, r *http.Request) {
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if instanceID == "" {
		writeDashboardError(w, 400, "instance_id_required")
		return
	}
	prices, err := h.Store.ListBillingPrices(r.Context(), instanceID)
	if err != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	metadata, err := h.Store.ListBillingModelMetadata(r.Context(), instanceID)
	if err != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	items := map[string]*billingModelItem{}
	today := time.Now().Format("2006-01-02")
	for _, p := range prices {
		item := items[p.ModelName]
		if item == nil {
			item = &billingModelItem{ModelName: p.ModelName}
			items[p.ModelName] = item
		}
		day := p.EffectiveFrom.Format("2006-01-02")
		if p.TierFrom == 0 && day <= today && (item.EffectiveFrom == "" || day > item.EffectiveFrom) {
			item.Input = p.Input
			item.Output = p.Output
			item.Cache = p.Cache
			item.EffectiveFrom = day
			item.PriceSource = "ct"
		}
	}
	for _, m := range metadata {
		if !m.Available {
			continue
		}
		item := items[m.ModelName]
		if item == nil {
			item = &billingModelItem{ModelName: m.ModelName}
			items[m.ModelName] = item
		}
		item.MaxContextTokens = m.MaxContextTokens
	}
	out := make([]billingModelItem, 0, len(items))
	for _, item := range items {
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModelName < out[j].ModelName })
	writeDashboardJSON(w, 200, map[string]any{"items": out})
}

func (h BillingModelsHandler) sync(w http.ResponseWriter, r *http.Request) {
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if instanceID == "" || h.Source == nil {
		writeDashboardError(w, 400, "instance_id_required")
		return
	}
	models, err := h.Source.ConfiguredModels(r.Context(), instanceID)
	if err != nil {
		writeDashboardError(w, 502, "newapi_models_query_failed")
		return
	}
	raw, err := h.Source.RatioSnapshot(r.Context(), instanceID)
	if err != nil {
		writeDashboardError(w, 502, "newapi_ratio_query_failed")
		return
	}
	snapshot, err := billing.ParseRatioSnapshot(raw)
	if err != nil {
		writeDashboardError(w, 422, "newapi_ratio_invalid")
		return
	}
	actor := ctauth.Actor(r)
	if actor == "" {
		actor = "legacy-admin"
	}
	now := time.Now().UTC()
	if err = h.Store.UpsertBillingModels(r.Context(), instanceID, models, now, actor); err != nil {
		writeDashboardError(w, 500, "model_sync_failed")
		return
	}
	current, err := h.Store.ListBillingPrices(r.Context(), instanceID)
	if err != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	changed := 0
	for _, model := range models {
		price, _, priceErr := billing.FallbackPrice(snapshot, model, "")
		if priceErr != nil || sameCurrentBasePrice(current, model, now, price) {
			continue
		}
		record := billing.PriceRecord{InstanceID: instanceID, ModelName: model, Price: billing.Price{EffectiveFrom: billingSyncDay(now), TierFrom: 0, Input: price.Input, Output: price.Output, Cache: price.Cache}, UpdatedAt: now, UpdatedBy: actor}
		if err = h.Store.PutBillingPriceSchedule(r.Context(), []billing.PriceRecord{record}); err != nil {
			writeDashboardError(w, 500, "price_sync_failed")
			return
		}
		changed++
	}
	billing.MonthlySummaryCache.InvalidateInstance(instanceID)
	billingConfigAudit(h.Store, r, instanceID, "billing.models_sync", instanceID, map[string]any{"models": len(models), "prices_changed": changed})
	writeDashboardJSON(w, 200, map[string]any{"models": len(models), "prices_changed": changed})
}

func billingSyncDay(now time.Time) time.Time {
	y, m, d := now.In(time.Local).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

func sameCurrentBasePrice(records []billing.PriceRecord, model string, now time.Time, price billing.Price) bool {
	var latest *billing.PriceRecord
	day := billingSyncDay(now)
	for i := range records {
		r := &records[i]
		if r.ModelName != model || r.TierFrom != 0 || r.EffectiveFrom.After(day) {
			continue
		}
		if latest == nil || r.EffectiveFrom.After(latest.EffectiveFrom) {
			latest = r
		}
	}
	return latest != nil && latest.Input == price.Input && latest.Output == price.Output && latest.Cache == price.Cache
}
