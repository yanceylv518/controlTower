package dashboard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
	"controltower/server/internal/storage"
)

type BillingBackfillStore interface {
	InsertOperationAudit(storage.OperationAudit) error
}
type BillingRollupper interface {
	RollupDay(context.Context, string, time.Time) (billing.RollupResult, error)
}

type BillingBackfillHandler struct {
	Rollup BillingRollupper
	Audit  BillingBackfillStore
	Sleep  func(time.Duration)
}

type billingBackfillRequest struct {
	InstanceID string `json:"instance_id"`
	From       string `json:"from"`
	To         string `json:"to"`
	Force      bool   `json:"force"`
	Scope      string `json:"scope"`
}

func (h BillingBackfillHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	if user, ok := ctauth.CurrentUser(r); ok && user.Role != "admin" {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	var req billingBackfillRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.InstanceID == "" {
		writeDashboardError(w, 400, "invalid_request")
		return
	}
	from, err := time.ParseInLocation("2006-01-02", req.From, billing.BusinessLocation)
	if err != nil {
		writeDashboardError(w, 400, "invalid_from")
		return
	}
	to, err := time.ParseInLocation("2006-01-02", req.To, billing.BusinessLocation)
	if err != nil || to.Before(from) || to.Sub(from) > 366*24*time.Hour {
		writeDashboardError(w, 400, "invalid_to")
		return
	}
	if h.Rollup == nil {
		writeDashboardError(w, 500, "billing_not_configured")
		return
	}
	results := make([]billing.RollupResult, 0, int(to.Sub(from)/(24*time.Hour))+1)
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		result, rollupErr := h.Rollup.RollupDay(r.Context(), req.InstanceID, day)
		if rollupErr != nil {
			writeDashboardJSON(w, 502, map[string]any{"error": "backfill_failed", "day": day.Format("2006-01-02")})
			return
		}
		results = append(results, result)
		if day.Before(to) {
			if h.Sleep != nil {
				h.Sleep(500 * time.Millisecond)
			} else {
				time.Sleep(500 * time.Millisecond)
			}
		}
	}
	h.audit(r, req, len(results))
	writeDashboardJSON(w, 200, map[string]any{"accepted": true, "days": len(results), "results": results})
}

func (h BillingBackfillHandler) audit(r *http.Request, req billingBackfillRequest, days int) {
	if h.Audit == nil {
		return
	}
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	after, _ := json.Marshal(map[string]any{"from": req.From, "to": req.To, "days": days})
	_ = h.Audit.InsertOperationAudit(storage.OperationAudit{ID: hex.EncodeToString(raw), InstanceID: req.InstanceID, OperationType: "billing.backfill", TargetType: "billing_daily", TargetID: req.InstanceID, ActorID: ctauth.Actor(r), AfterSummary: string(after), Status: "succeeded", CreatedAt: time.Now().UTC()})
}
