package dashboard

import (
	"context"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
	"net/http"
)

func billingTypeAllowed(r *http.Request, jobType string) bool {
	u, authenticated := ctauth.CurrentUser(r)
	if !authenticated {
		return true
	} // Token authentication and direct handler tests retain their existing checks.
	if ctauth.HasPermission(u, "billing.tasks") {
		return true
	}
	switch jobType {
	case "user_statement":
		return ctauth.HasPermission(u, "billing.users")
	case "upstream_statement":
		return ctauth.HasPermission(u, "billing.channels")
	default:
		return false
	}
}

// Resolve the persisted job type, never a caller-supplied type, before accessing a shared job endpoint.
func requireBillingJobPermission(w http.ResponseWriter, r *http.Request, store interface {
	BillingJob(context.Context, string) (billing.Job, error)
}, id string) bool {
	u, authenticated := ctauth.CurrentUser(r)
	if !authenticated || ctauth.HasPermission(u, "billing.tasks") {
		return true
	}
	job, err := store.BillingJob(r.Context(), id)
	if err != nil || !billingTypeAllowed(r, job.JobType) {
		writeDashboardError(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
}
