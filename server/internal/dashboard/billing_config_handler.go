package dashboard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"controltower/server/internal/auditmeta"
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
	default:
		writeDashboardError(w, 405, "method_not_allowed")
	}
}

type billingAuditStore interface {
	InsertOperationAudit(storage.OperationAudit) error
}

func billingConfigAudit(store billingAuditStore, r *http.Request, instanceID, operation, target string, after any) {
	_ = auditBillingMutation(store, r, instanceID, operation, target, nil, after)
}

func auditBillingMutation(store billingAuditStore, r *http.Request, instanceID, operation, target string, before, after any) error {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	beforeBody, err := json.Marshal(before)
	if err != nil {
		return err
	}
	afterBody, err := json.Marshal(after)
	if err != nil {
		return err
	}
	actor := ctauth.Actor(r)
	if actor == "" {
		actor = "legacy-admin"
	}
	now := time.Now().UTC()
	entry := storage.OperationAudit{ID: hex.EncodeToString(raw), InstanceID: instanceID, OperationType: operation, TargetType: "billing", TargetID: target, ActorID: actor, BeforeSummary: string(beforeBody), AfterSummary: string(afterBody), Status: "succeeded", CreatedAt: now, UpdatedAt: now}
	auditmeta.Enrich(r, &entry)
	if err := store.InsertOperationAudit(entry); err != nil {
		return err
	}
	auditmeta.MarkSemanticAudit(r)
	return nil
}
