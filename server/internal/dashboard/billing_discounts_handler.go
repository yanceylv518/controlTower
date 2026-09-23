package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
)

type BillingDiscountStore interface {
	ListBillingDiscountRules(context.Context, string, string) ([]billing.DiscountRule, error)
	PutBillingDiscountRule(context.Context, billing.DiscountRule) (billing.DiscountRule, error)
	DeleteBillingDiscountRule(context.Context, string, int64) error
}

type BillingDiscountHandler struct{ Store BillingDiscountStore }

func (h BillingDiscountHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !billingAdminAllowed(r) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	switch r.Method {
	case http.MethodGet:
		site, kind := strings.TrimSpace(r.URL.Query().Get("instance_id")), strings.TrimSpace(r.URL.Query().Get("type"))
		if site == "" || (kind != "" && kind != billing.DiscountUpstreamChannel) {
			writeDashboardError(w, 400, "invalid_discount_query")
			return
		}
		kind = billing.DiscountUpstreamChannel
		items, err := h.Store.ListBillingDiscountRules(r.Context(), site, kind)
		if err != nil {
			writeDashboardError(w, 500, "billing_discounts_query_failed")
			return
		}
		writeDashboardJSON(w, 200, map[string]any{"items": items})
	case http.MethodPost, http.MethodPut:
		var v billing.DiscountRule
		if json.NewDecoder(r.Body).Decode(&v) != nil {
			writeDashboardError(w, 400, "invalid_discount")
			return
		}
		v.InstanceID = strings.TrimSpace(v.InstanceID)
		v.ModelName = strings.TrimSpace(v.ModelName)
		v.Remark = strings.TrimSpace(v.Remark)
		d, ok := new(big.Rat).SetString(v.Discount)
		if v.InstanceID == "" || v.SubjectID <= 0 || !ok || d.Sign() <= 0 || d.Cmp(big.NewRat(1, 1)) > 0 || v.EffectiveFrom.IsZero() || (v.EffectiveTo != nil && !v.EffectiveTo.After(v.EffectiveFrom)) {
			writeDashboardError(w, 400, "invalid_discount")
			return
		}
		if v.DiscountType == billing.DiscountUpstreamChannel {
			v.ModelName = ""
			if v.ChannelID <= 0 {
				writeDashboardError(w, 400, "discount_channel_required")
				return
			}
		} else {
			writeDashboardError(w, 400, "invalid_discount_type")
			return
		}
		if r.Method == http.MethodPost {
			v.ID = 0
		} else if v.ID <= 0 {
			writeDashboardError(w, 400, "discount_id_required")
			return
		}
		var before *billing.DiscountRule
		if v.ID > 0 {
			current, err := h.Store.ListBillingDiscountRules(r.Context(), v.InstanceID, v.DiscountType)
			if err != nil {
				writeDashboardError(w, 500, "billing_discounts_query_failed")
				return
			}
			for index := range current {
				if current[index].ID == v.ID {
					before = &current[index]
					break
				}
			}
		}
		v.UpdatedBy = ctauth.Actor(r)
		saved, err := h.Store.PutBillingDiscountRule(r.Context(), v)
		if errors.Is(err, billing.ErrDiscountOverlap) {
			writeDashboardError(w, 409, "billing_discount_overlap")
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeDashboardError(w, 404, "billing_discount_target_not_found")
			return
		}
		if err != nil {
			writeDashboardError(w, 500, "billing_discount_save_failed")
			return
		}
		if audit, ok := any(h.Store).(billingAuditStore); ok {
			beforeValue := any(map[string]any{})
			operation := "billing.discount.create"
			if before != nil {
				beforeValue = *before
				operation = "billing.discount.update"
			}
			if err := auditBillingMutation(audit, r, saved.InstanceID, operation, strconv.FormatInt(saved.ID, 10), beforeValue, saved); err != nil {
				writeDashboardError(w, 500, "billing_discount_audit_failed")
				return
			}
		}
		writeDashboardJSON(w, 200, saved)
	case http.MethodDelete:
		site := strings.TrimSpace(r.URL.Query().Get("instance_id"))
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if site == "" || id <= 0 {
			writeDashboardError(w, 400, "invalid_discount")
			return
		}
		current, err := h.Store.ListBillingDiscountRules(r.Context(), site, billing.DiscountUpstreamChannel)
		if err != nil {
			writeDashboardError(w, 500, "billing_discounts_query_failed")
			return
		}
		var before *billing.DiscountRule
		for index := range current {
			if current[index].ID == id {
				before = &current[index]
				break
			}
		}
		if err := h.Store.DeleteBillingDiscountRule(r.Context(), site, id); err != nil {
			writeDashboardError(w, 404, "billing_discount_not_found")
			return
		}
		if audit, ok := any(h.Store).(billingAuditStore); ok {
			beforeValue := any(map[string]any{})
			if before != nil {
				beforeValue = *before
			}
			if err := auditBillingMutation(audit, r, site, "billing.discount.delete", strconv.FormatInt(id, 10), beforeValue, map[string]any{"deleted": true}); err != nil {
				writeDashboardError(w, 500, "billing_discount_audit_failed")
				return
			}
		}
		writeDashboardJSON(w, 200, map[string]any{"deleted": true})
	default:
		writeDashboardError(w, 405, "method_not_allowed")
	}
}
