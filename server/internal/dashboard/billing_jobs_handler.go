package dashboard

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
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

type BillingJobsHandler struct{ Store BillingJobsStore }

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
	from, e1 := time.ParseInLocation("2006-01-02", req.From, time.Local)
	through, e2 := time.ParseInLocation("2006-01-02", req.To, time.Local)
	if e1 != nil || e2 != nil {
		writeDashboardError(w, 400, "invalid_range")
		return
	}
	to := through.AddDate(0, 0, 1)
	requestKey := billingRequestKey(req.InstanceID, from, to)
	if !req.Force {
		existing, findErr := h.Store.BillingJobByRequestKey(r.Context(), requestKey)
		if findErr == nil {
			writeDashboardJSON(w, http.StatusOK, map[string]any{"accepted": true, "reused": true, "job": existing})
			return
		}
		if findErr != sql.ErrNoRows {
			writeDashboardError(w, 500, "billing_job_query_failed")
			return
		}
	}
	job, steps, err := billing.NewJob(req.InstanceID, from, to, ctauth.Actor(r))
	if err != nil {
		writeDashboardError(w, 400, "invalid_range")
		return
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

func billingRequestKey(instanceID string, from, to time.Time) string {
	sum := sha256.Sum256([]byte(instanceID + "|generate|" + from.Format("2006-01-02") + "|" + to.Format("2006-01-02")))
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
