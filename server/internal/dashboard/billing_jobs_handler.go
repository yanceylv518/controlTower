package dashboard

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
)

type BillingJobsStore interface {
	CreateBillingJob(context.Context, billing.Job, []billing.JobStep) error
	BillingJob(context.Context, string) (billing.Job, error)
	BillingJobByRequestKey(context.Context, string) (billing.Job, error)
}

type BillingJobsPreflightStore interface {
	UpsertBillingModels(context.Context, string, []string, time.Time, string) error
	ListBillingModelMetadata(context.Context, string) ([]billing.ModelMetadata, error)
}

type BillingJobsHandler struct {
	Store     BillingJobsStore
	Preflight BillingJobsPreflightStore
	Source    BillingModelSource
}

func (h BillingJobsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		job, err := h.Store.BillingJob(r.Context(), strings.TrimSpace(r.URL.Query().Get("id")))
		if err == sql.ErrNoRows {
			writeDashboardError(w, 404, "job_not_found")
			return
		}
		if err != nil {
			writeDashboardError(w, 500, "billing_job_query_failed")
			return
		}
		if !billingSiteAllowed(r, job.InstanceID, 0) {
			writeDashboardError(w, 403, "forbidden")
			return
		}
		writeDashboardJSON(w, 200, job)
		return
	}
	if r.Method != http.MethodPost {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	if user, ok := ctauth.CurrentUser(r); ok && user.Role != "admin" {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	var req billingBackfillRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeDashboardError(w, 400, "invalid_request")
		return
	}
	from, to, rangeErr := parseBillingInputRange(req.From, req.To)
	if rangeErr != nil {
		writeDashboardError(w, 400, "invalid_range")
		return
	}
	scope := strings.TrimSpace(req.Scope)
	if scope == "" {
		scope = "all"
	}
	if scope != "all" && scope != "channel" {
		writeDashboardError(w, 400, "invalid_scope")
		return
	}
	requestKey := billingRequestKey(req.InstanceID, scope, from, to)
	if !req.Force {
		existing, findErr := h.Store.BillingJobByRequestKey(r.Context(), requestKey)
		if findErr == nil {
			if existing.Status != "failed" {
				writeDashboardJSON(w, http.StatusOK, map[string]any{"accepted": true, "reused": true, "job": existing})
				return
			}
			req.Force = true
		}
		if findErr != nil && findErr != sql.ErrNoRows {
			writeDashboardError(w, 500, "billing_job_query_failed")
			return
		}
	}
	if h.Preflight != nil && h.Source != nil {
		models, syncErr := h.Source.ConfiguredModels(r.Context(), req.InstanceID)
		if syncErr != nil {
			writeDashboardError(w, 502, "newapi_models_query_failed")
			return
		}
		actor := ctauth.Actor(r)
		if actor == "" {
			actor = "legacy-admin"
		}
		if syncErr = h.Preflight.UpsertBillingModels(r.Context(), req.InstanceID, models, time.Now().UTC(), actor); syncErr != nil {
			writeDashboardError(w, 500, "model_sync_failed")
			return
		}
		metadata, metadataErr := h.Preflight.ListBillingModelMetadata(r.Context(), req.InstanceID)
		if metadataErr != nil {
			writeDashboardError(w, 500, "model_metadata_query_failed")
			return
		}
		maxByModel := make(map[string]int64, len(metadata))
		for _, item := range metadata {
			maxByModel[item.ModelName] = item.MaxContextTokens
		}
		missing := make([]string, 0)
		for _, model := range models {
			if maxByModel[model] <= 0 {
				missing = append(missing, model)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			writeDashboardJSON(w, http.StatusConflict, map[string]any{"error": "billing_model_context_missing", "models": missing})
			return
		}
	}
	job, steps, err := billing.NewJob(req.InstanceID, from, to, ctauth.Actor(r))
	if err != nil {
		writeDashboardError(w, 400, "invalid_range")
		return
	}
	if scope == "channel" {
		job.JobType = "channel_generate"
	}
	if req.Force {
		job.RequestKey = requestKey + ":force:" + job.ID
	} else {
		job.RequestKey = requestKey
	}
	if err = h.Store.CreateBillingJob(r.Context(), job, steps); err != nil {
		if !req.Force {
			if existing, findErr := h.Store.BillingJobByRequestKey(r.Context(), requestKey); findErr == nil {
				writeDashboardJSON(w, http.StatusOK, map[string]any{"accepted": true, "reused": true, "job": existing})
				return
			}
		}
		writeDashboardError(w, 500, "billing_job_create_failed")
		return
	}
	writeDashboardJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "reused": false, "job": job})
}

func parseBillingInputRange(fromRaw, toRaw string) (time.Time, time.Time, error) {
	parse := func(raw string) (time.Time, bool, error) {
		if value, err := time.ParseInLocation("2006-01-02 15:04:05", raw, billing.BusinessLocation); err == nil {
			return value, true, nil
		}
		value, err := time.ParseInLocation("2006-01-02", raw, billing.BusinessLocation)
		return value, false, err
	}
	from, _, fromErr := parse(strings.TrimSpace(fromRaw))
	through, _, toErr := parse(strings.TrimSpace(toRaw))
	if fromErr != nil || toErr != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid billing range")
	}
	// Billing ranges are consistently half-open: [from, to). The selected end
	// instant is never included in generation, reports, or anomaly exports.
	to := through
	if !to.After(from) || to.Sub(from) > 60*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid billing range")
	}
	return from, to, nil
}

func billingRequestKey(instanceID, scope string, from, to time.Time) string {
	// Include the calculation version so a billing-rule correction invalidates
	// previously completed jobs once, while identical jobs on the current
	// algorithm are still reused.
	const calculationVersion = "v6-anomaly-actual-amount"
	sum := sha256.Sum256([]byte(calculationVersion + "|" + instanceID + "|generate|" + scope + "|" + from.Format(time.RFC3339Nano) + "|" + to.Format(time.RFC3339Nano)))
	return "billing:" + fmt.Sprintf("%x", sum[:16])
}

type BillingUserSettingsStore interface {
	ListBillingUserSettings(context.Context, string) (map[int64]billing.UserSetting, error)
	PutBillingUserSetting(context.Context, billing.UserSetting) error
}
type BillingUserSettingsHandler struct{ Store BillingUserSettingsStore }

func (h BillingUserSettingsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		site := strings.TrimSpace(r.URL.Query().Get("instance_id"))
		if !billingSiteAllowed(r, site, 0) {
			writeDashboardError(w, 403, "forbidden")
			return
		}
		items, err := h.Store.ListBillingUserSettings(r.Context(), site)
		if err != nil {
			writeDashboardError(w, 500, "billing_settings_query_failed")
			return
		}
		writeDashboardJSON(w, 200, map[string]any{"items": items})
		return
	}
	if r.Method != http.MethodPut {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	if user, ok := ctauth.CurrentUser(r); ok && user.Role != "admin" {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	var item billing.UserSetting
	if json.NewDecoder(r.Body).Decode(&item) != nil || item.InstanceID == "" || item.UserID <= 0 {
		writeDashboardError(w, 400, "invalid_request")
		return
	}
	item.UpdatedAt, item.UpdatedBy = time.Now().UTC(), ctauth.Actor(r)
	if err := h.Store.PutBillingUserSetting(r.Context(), item); err != nil {
		writeDashboardError(w, 500, "billing_setting_save_failed")
		return
	}
	writeDashboardJSON(w, 200, item)
}

func billingSiteAllowed(r *http.Request, site string, userID int64) bool {
	if site == "" {
		return false
	}
	user, ok := ctauth.CurrentUser(r)
	if !ok || user.Role == "admin" {
		return true
	}
	if user.ScopeSite != site {
		return false
	}
	return userID == 0 || containsBillingUser(user.ScopeUserIDs, userID)
}
