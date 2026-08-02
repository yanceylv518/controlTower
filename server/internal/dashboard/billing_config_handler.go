package dashboard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
	"controltower/server/internal/storage"
)

type BillingConfigStore interface {
	ListBillingPrices(context.Context, string) ([]billing.PriceRecord, error)
	PutBillingPriceSchedule(context.Context, []billing.PriceRecord) error
	ListBillingGroupRatios(context.Context, string) ([]billing.GroupRatio, error)
	PutBillingGroupRatio(context.Context, billing.GroupRatio) error
	InsertOperationAudit(storage.OperationAudit) error
}

type BillingPricesHandler struct{ Store BillingConfigStore }
type BillingGroupRatiosHandler struct{ Store BillingConfigStore }

type billingTierRequest struct {
	TierFrom int64  `json:"tier_from"`
	Input    string `json:"input_price"`
	Output   string `json:"output_price"`
	Cache    string `json:"cache_price"`
}
type billingPriceRequest struct {
	InstanceID    string               `json:"instance_id"`
	ModelName     string               `json:"model_name"`
	EffectiveFrom string               `json:"effective_from"`
	Tiers         []billingTierRequest `json:"tiers"`
}
type billingRatioRequest struct {
	InstanceID string `json:"instance_id"`
	GroupName  string `json:"group_name"`
	Ratio      string `json:"ratio"`
}

func billingAdminAllowed(r *http.Request) bool {
	user, ok := ctauth.CurrentUser(r)
	return !ok || user.Role == "admin"
}

func (h BillingPricesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		writeDashboardError(w, 500, "billing_not_configured")
		return
	}
	if !billingAdminAllowed(r) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	switch r.Method {
	case http.MethodGet:
		instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
		if instanceID == "" {
			writeDashboardError(w, 400, "instance_id_required")
			return
		}
		items, err := h.Store.ListBillingPrices(r.Context(), instanceID)
		if err != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		writeDashboardJSON(w, 200, map[string]any{"items": items})
	case http.MethodPut:
		var req billingPriceRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeDashboardError(w, 400, "invalid_json")
			return
		}
		day, err := time.ParseInLocation("2006-01-02", req.EffectiveFrom, time.Local)
		if err != nil || strings.TrimSpace(req.InstanceID) == "" || strings.TrimSpace(req.ModelName) == "" || len(req.Tiers) == 0 {
			writeDashboardError(w, 400, "invalid_price_schedule")
			return
		}
		now, actor := time.Now().UTC(), ctauth.Actor(r)
		if actor == "" {
			actor = "legacy-admin"
		}
		records := make([]billing.PriceRecord, 0, len(req.Tiers))
		for _, tier := range req.Tiers {
			price := billing.Price{EffectiveFrom: day, TierFrom: tier.TierFrom, Input: tier.Input, Output: tier.Output, Cache: tier.Cache}
			if billing.ValidatePrice(price) != nil {
				writeDashboardError(w, 400, "invalid_price")
				return
			}
			records = append(records, billing.PriceRecord{InstanceID: req.InstanceID, ModelName: req.ModelName, Price: price, UpdatedAt: now, UpdatedBy: actor})
		}
		if billing.ValidateTierSchedule(priceValuesForHandler(records)) != nil {
			writeDashboardError(w, 400, "invalid_tiers")
			return
		}
		if err = h.Store.PutBillingPriceSchedule(r.Context(), records); err != nil {
			writeDashboardError(w, 500, "update_failed")
			return
		}
		billing.MonthlySummaryCache.InvalidateInstance(req.InstanceID)
		billingConfigAudit(h.Store, r, req.InstanceID, "billing.price_update", req.ModelName, req)
		writeDashboardJSON(w, 200, map[string]any{"items": records})
	default:
		writeDashboardError(w, 405, "method_not_allowed")
	}
}

func (h BillingGroupRatiosHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		writeDashboardError(w, 500, "billing_not_configured")
		return
	}
	if !billingAdminAllowed(r) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	switch r.Method {
	case http.MethodGet:
		instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
		if instanceID == "" {
			writeDashboardError(w, 400, "instance_id_required")
			return
		}
		items, err := h.Store.ListBillingGroupRatios(r.Context(), instanceID)
		if err != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		writeDashboardJSON(w, 200, map[string]any{"items": items})
	case http.MethodPut:
		var req billingRatioRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil || strings.TrimSpace(req.InstanceID) == "" || strings.TrimSpace(req.GroupName) == "" || billing.ValidateRatio(req.Ratio) != nil {
			writeDashboardError(w, 400, "invalid_group_ratio")
			return
		}
		actor := ctauth.Actor(r)
		if actor == "" {
			actor = "legacy-admin"
		}
		value := billing.GroupRatio{InstanceID: req.InstanceID, GroupName: req.GroupName, Ratio: req.Ratio, UpdatedAt: time.Now().UTC(), UpdatedBy: actor}
		if err := h.Store.PutBillingGroupRatio(r.Context(), value); err != nil {
			writeDashboardError(w, 500, "update_failed")
			return
		}
		billing.MonthlySummaryCache.InvalidateInstance(req.InstanceID)
		billingConfigAudit(h.Store, r, req.InstanceID, "billing.group_ratio_update", req.GroupName, req)
		writeDashboardJSON(w, 200, value)
	default:
		writeDashboardError(w, 405, "method_not_allowed")
	}
}

func priceValuesForHandler(records []billing.PriceRecord) []billing.Price {
	values := make([]billing.Price, len(records))
	for i := range records {
		values[i] = records[i].Price
	}
	return values
}
func billingConfigAudit(store BillingConfigStore, r *http.Request, instanceID, operation, target string, after any) {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	body, _ := json.Marshal(after)
	actor := ctauth.Actor(r)
	if actor == "" {
		actor = "legacy-admin"
	}
	_ = store.InsertOperationAudit(storage.OperationAudit{ID: hex.EncodeToString(raw), InstanceID: instanceID, OperationType: operation, TargetType: "billing", TargetID: target, ActorID: actor, AfterSummary: string(body), Status: "succeeded", CreatedAt: time.Now().UTC()})
}
