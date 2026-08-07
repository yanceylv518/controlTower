package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
)

// BillingVerificationStore persists an asynchronous, immutable verification of
// one completed user-billing generation job.
type BillingVerificationStore interface {
	BillingJob(context.Context, string) (billing.Job, error)
	CreateBillingVerificationJob(context.Context, billing.Job, []billing.JobStep, string) error
	LatestBillingVerificationJob(context.Context, string) (billing.Job, error)
	VerificationSourceJob(context.Context, string) (billing.Job, error)
	BillingVerificationResults(context.Context, string, bool, int, int) ([]billing.VerificationResult, billing.VerificationSummary, int, error)
}

type BillingVerificationHandler struct{ Store BillingVerificationStore }

type billingVerificationRequest struct {
	SourceJobID string `json:"source_job_id"`
}

func (h BillingVerificationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !billingReconciliationAdmin(r) {
		writeDashboardError(w, http.StatusForbidden, "forbidden")
		return
	}
	switch r.Method {
	case http.MethodPost:
		h.create(w, r)
	case http.MethodGet:
		h.get(w, r)
	default:
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h BillingVerificationHandler) create(w http.ResponseWriter, r *http.Request) {
	var req billingVerificationRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil || strings.TrimSpace(req.SourceJobID) == "" {
		writeDashboardError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	source, err := h.validSourceJob(r, strings.TrimSpace(req.SourceJobID))
	if err != nil {
		writeBillingVerificationSourceError(w, err)
		return
	}
	if existing, findErr := h.Store.LatestBillingVerificationJob(r.Context(), source.ID); findErr == nil && existing.Status != "failed" {
		writeDashboardJSON(w, http.StatusOK, map[string]any{"accepted": true, "reused": true, "job": existing})
		return
	} else if findErr != nil && findErr != sql.ErrNoRows {
		writeDashboardError(w, http.StatusInternalServerError, "billing_verification_query_failed")
		return
	}
	job, steps, err := billing.NewVerificationJob(source, ctauth.Actor(r))
	if err != nil {
		writeDashboardError(w, http.StatusBadRequest, "invalid_source_job")
		return
	}
	if err = h.Store.CreateBillingVerificationJob(r.Context(), job, steps, source.ID); err != nil {
		if err == billing.ErrVerificationAlreadyExists {
			if existing, findErr := h.Store.LatestBillingVerificationJob(r.Context(), source.ID); findErr == nil && existing.Status != "failed" {
				writeDashboardJSON(w, http.StatusOK, map[string]any{"accepted": true, "reused": true, "job": existing})
				return
			}
		}
		writeDashboardError(w, http.StatusInternalServerError, "billing_verification_create_failed")
		return
	}
	writeDashboardJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "reused": false, "job": job})
}

func (h BillingVerificationHandler) get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sourceID := strings.TrimSpace(q.Get("source_job_id"))
	verificationID := strings.TrimSpace(q.Get("job_id"))
	if sourceID == "" {
		writeDashboardError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	source, err := h.validSourceJob(r, sourceID)
	if err != nil {
		writeBillingVerificationSourceError(w, err)
		return
	}
	var job billing.Job
	if verificationID == "" {
		job, err = h.Store.LatestBillingVerificationJob(r.Context(), source.ID)
	} else {
		job, err = h.Store.BillingJob(r.Context(), verificationID)
		if err == nil {
			linked, linkErr := h.Store.VerificationSourceJob(r.Context(), job.ID)
			if linkErr != nil || linked.ID != source.ID {
				err = sql.ErrNoRows
			}
		}
	}
	if err == sql.ErrNoRows {
		writeDashboardJSON(w, http.StatusOK, map[string]any{"job": nil, "items": []billing.VerificationResult{}, "summary": billing.VerificationSummary{}, "total": 0, "page": 1, "page_size": 50})
		return
	}
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "billing_verification_query_failed")
		return
	}
	page, pageSize := positiveInt(q.Get("page"), 1), positiveInt(q.Get("page_size"), 50)
	if pageSize > 200 {
		pageSize = 200
	}
	items := []billing.VerificationResult{}
	summary := billing.VerificationSummary{}
	total := 0
	if job.Status == "complete" {
		items, summary, total, err = h.Store.BillingVerificationResults(r.Context(), job.ID, q.Get("mismatches_only") != "false", pageSize, (page-1)*pageSize)
		if err != nil {
			writeDashboardError(w, http.StatusInternalServerError, "billing_verification_query_failed")
			return
		}
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"job": job, "items": items, "summary": summary, "total": total, "page": page, "page_size": pageSize})
}

func (h BillingVerificationHandler) validSourceJob(r *http.Request, id string) (billing.Job, error) {
	job, err := h.Store.BillingJob(r.Context(), id)
	if err != nil {
		return job, err
	}
	if job.JobType != "generate" || job.Status != "complete" || !billingSiteAllowed(r, job.InstanceID, 0) {
		return billing.Job{}, sql.ErrNoRows
	}
	return job, nil
}

func writeBillingVerificationSourceError(w http.ResponseWriter, err error) {
	if err == sql.ErrNoRows {
		writeDashboardError(w, http.StatusConflict, "billing_source_job_unavailable")
		return
	}
	writeDashboardError(w, http.StatusInternalServerError, "billing_verification_query_failed")
}
