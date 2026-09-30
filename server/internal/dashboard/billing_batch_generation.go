package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type BillingBatchGenerationStore interface {
	PutBillingAutomaticTargets(context.Context, []billing.AutomaticTarget) error
	BillingGenerationProgress(context.Context, billing.AutomaticTarget) (billing.GenerationProgress, error)
}
type BillingBatchGenerationHandler struct {
	Store      BillingBatchGenerationStore
	Automation BillingAutomation
}

var billingBatchWake = make(chan struct{}, 1)

func (h BillingBatchGenerationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !billingAdminAllowed(r) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	var req struct {
		Kind       string    `json:"kind"`
		InstanceID string    `json:"instance_id"`
		Overwrite  bool      `json:"overwrite"`
		SubjectIDs []int64   `json:"subject_ids"`
		From       time.Time `json:"from"`
		To         time.Time `json:"to"`
	}
	if r.Method == "POST" {
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req) != nil {
			writeDashboardError(w, 400, "invalid_request")
			return
		}
		if !validateBillingBatchKind(w, r, &req.Kind) {
			return
		}
	} else if r.Method == "GET" {
		req.InstanceID = r.URL.Query().Get("instance_id")
		req.Kind = r.URL.Query().Get("kind")
		if !validateBillingBatchKind(w, r, &req.Kind) {
			return
		}
		if r.URL.Query().Get("action") == "history" || r.URL.Query().Get("action") == "history-detail" {
			h.serveTaskHistory(w, r, req.InstanceID, req.Kind)
			return
		}
		if r.URL.Query().Get("action") == "status" {
			if req.InstanceID == "" || !billingSiteAllowed(r, req.InstanceID, 0) {
				writeDashboardError(w, 403, "forbidden")
				return
			}
			checker, ok := h.Store.(interface {
				BillingGenerationState(context.Context, string) (billing.GenerationState, error)
			})
			if !ok {
				writeDashboardError(w, 500, "billing_progress_unavailable")
				return
			}
			state, err := checker.BillingGenerationState(r.Context(), req.InstanceID)
			if err != nil {
				writeDashboardError(w, 500, "billing_progress_unavailable")
				return
			}
			// Return only task details that this administrator is allowed to inspect.
			targets := []billing.AutomaticTarget{}
			for _, t := range state.Targets {
				if t.Kind == req.Kind && billingTypeAllowed(r, t.Kind) && billingBatchSubjectAllowed(r, t.InstanceID, t.Kind, t.SubjectID) {
					targets = append(targets, t)
				}
			}
			state.Targets = targets
			jobs := []billing.Job{}
			for _, j := range state.Jobs {
				if j.JobType == req.Kind && billingTypeAllowed(r, j.JobType) && billingSiteAllowed(r, j.InstanceID, j.UserID) {
					jobs = append(jobs, j)
				}
			}
			state.Jobs = jobs
			writeDashboardJSON(w, 200, state)
			return
		}
		var err error
		req.From, err = time.Parse(time.RFC3339, r.URL.Query().Get("from"))
		if err != nil {
			writeDashboardError(w, 400, "invalid_billing_month")
			return
		}
		req.To, err = time.Parse(time.RFC3339, r.URL.Query().Get("to"))
		if err != nil {
			writeDashboardError(w, 400, "invalid_billing_month")
			return
		}
		for _, raw := range strings.Split(r.URL.Query().Get("subject_ids"), ",") {
			id, e := strconv.ParseInt(raw, 10, 64)
			if e != nil {
				writeDashboardError(w, 400, "invalid_users")
				return
			}
			req.SubjectIDs = append(req.SubjectIDs, id)
		}
	} else {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	from := req.From.In(billing.BusinessLocation)
	if req.InstanceID == "" || req.From.IsZero() || !req.From.Equal(billing.CompleteDayBoundary(req.From)) || from.Day() != 1 || !req.To.Equal(from.AddDate(0, 1, 0)) || !from.Before(billing.CompleteDayBoundary(time.Now())) {
		writeDashboardError(w, 400, "invalid_billing_month")
		return
	}
	if len(req.SubjectIDs) == 0 || len(req.SubjectIDs) > 50 {
		writeDashboardError(w, 400, "invalid_users")
		return
	}
	targets := []billing.AutomaticTarget{}
	seen := map[int64]bool{}
	for _, id := range req.SubjectIDs {
		if id <= 0 {
			writeDashboardError(w, 400, "invalid_users")
			return
		}
		if !billingBatchSubjectAllowed(r, req.InstanceID, req.Kind, id) {
			writeDashboardError(w, 403, "forbidden")
			return
		}
		if !seen[id] {
			seen[id] = true
			targets = append(targets, billing.AutomaticTarget{InstanceID: req.InstanceID, Kind: req.Kind, SubjectID: id, From: from, To: req.To, Overwrite: req.Overwrite})
		}
	}
	if r.Method == "POST" && r.URL.Query().Get("action") == "cancel" {
		controller, ok := h.Store.(interface {
			CancelBillingGeneration(context.Context, []billing.AutomaticTarget) error
		})
		if !ok {
			writeDashboardError(w, 500, "billing_cancel_unavailable")
			return
		}
		if err := controller.CancelBillingGeneration(r.Context(), targets); err != nil {
			writeDashboardError(w, 500, "billing_job_cancel_failed")
			return
		}
		writeDashboardJSON(w, 200, map[string]any{"cancelled": true})
		return
	}
	if r.Method == "POST" {
		if req.Kind == "upstream_statement" {
			if store, ok := h.Store.(interface {
				BillingStatementUpstream(context.Context, string, int64) (billing.Upstream, error)
			}); ok {
				for _, t := range targets {
					up, err := store.BillingStatementUpstream(r.Context(), t.InstanceID, t.SubjectID)
					if err != nil || len(up.Channels) == 0 {
						writeDashboardError(w, 400, "upstream_not_found_or_empty")
						return
					}
				}
			}
		}
		if err := h.Store.PutBillingAutomaticTargets(r.Context(), targets); err != nil {
			if errors.Is(err, billing.ErrGenerationInProgress) {
				writeDashboardError(w, 409, "billing_generation_in_progress")
				return
			}
			writeDashboardError(w, 500, "billing_target_save_failed")
			return
		}
		// The persistent scheduler remains the recovery path if another batch is running.
		if h.Automation.Store != nil {
			select {
			case billingBatchWake <- struct{}{}:
				go func() {
					defer func() { <-billingBatchWake }()
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
					defer cancel()
					for _, t := range targets {
						if ctx.Err() != nil {
							return
						}
						if err := h.Automation.Fill(ctx, t); err != nil {
							log.Printf("billing batch subject=%d: %v", t.SubjectID, err)
						}
					}
				}()
			default:
			}
		}
	}
	if r.Method == "GET" {
		if resolver, ok := h.Store.(interface {
			BillingBatchMembers(context.Context, []billing.AutomaticTarget) ([]billing.AutomaticTarget, error)
		}); ok {
			members, e := resolver.BillingBatchMembers(r.Context(), targets)
			if e != nil {
				writeDashboardError(w, 500, "billing_progress_unavailable")
				return
			}
			visible := []billing.AutomaticTarget{}
			for _, t := range members {
				if billingBatchSubjectAllowed(r, t.InstanceID, t.Kind, t.SubjectID) {
					visible = append(visible, t)
				}
			}
			targets = visible
		}
	}
	items := []billing.GenerationProgress{}
	for _, t := range targets {
		p, err := h.Store.BillingGenerationProgress(r.Context(), t)
		if err != nil {
			writeDashboardError(w, 500, "billing_progress_unavailable")
			return
		}
		items = append(items, p)
	}
	status := 200
	if r.Method == "POST" {
		status = 202
	}
	ids := []int64{}
	for _, t := range targets {
		ids = append(ids, t.SubjectID)
	}
	writeDashboardJSON(w, status, map[string]any{"accepted": true, "items": items, "subject_ids": ids})
}

func validateBillingBatchKind(w http.ResponseWriter, r *http.Request, kind *string) bool {
	if *kind == "" {
		*kind = "user_statement"
	}
	if *kind != "user_statement" && *kind != "upstream_statement" {
		writeDashboardError(w, 400, "invalid_statement_type")
		return false
	}
	if !billingTypeAllowed(r, *kind) {
		writeDashboardError(w, 403, "forbidden")
		return false
	}
	return true
}
func billingBatchSubjectAllowed(r *http.Request, site, kind string, id int64) bool {
	if kind == "upstream_statement" {
		id = 0
	}
	return billingSiteAllowed(r, site, id)
}
