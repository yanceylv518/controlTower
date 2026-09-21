package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
	"controltower/server/internal/auth"
)

type ArchiveVerifyStore interface {
	CreateArchiveReconcile(context.Context, string, string, ac.ReconcileRequest, string) (ac.ReconcileTaskItem, error)
	ListArchiveReconciles(context.Context, string, string) ([]ac.ReconcileTaskItem, error)
	RetryArchiveReconcile(context.Context, string, string, string, string) (ac.ReconcileTaskItem, error)
	CreateArchiveSeal(context.Context, string, string, ac.SealRequest, string) (ac.SealTaskItem, error)
	ListArchiveSeals(context.Context, string, string) ([]ac.SealTaskItem, error)
	RetryArchiveSeal(context.Context, string, string, string, string) (ac.SealTaskItem, error)
}

type ArchiveVerifyHandler struct {
	Store     ArchiveVerifyStore
	Operation string
}

func (h ArchiveVerifyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.CurrentUser(r)
	if !ok || !auth.HasPermission(u, "archive.manage") {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	site, dataset := r.URL.Query().Get("site_id"), r.PathValue("dataset")
	if site == "" || len(site) > 64 {
		writeDashboardError(w, 400, "site_required")
		return
	}
	if _, err := af.IDBytes(dataset); err != nil {
		writeDashboardError(w, 400, "invalid_dataset_id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var result any
	var err error
	switch h.Operation {
	case "verify":
		if r.Method == http.MethodGet {
			var items []ac.ReconcileTaskItem
			items, err = h.Store.ListArchiveReconciles(ctx, site, dataset)
			result = map[string]any{"items": items, "archive_billing": false, "evidence_authority": "archive_readonly_catalog"}
		} else if r.Method == http.MethodPost {
			var request ac.ReconcileRequest
			if !archiveBackfillBody(w, r, &request) || !validArchiveVerificationRequest(request) {
				writeDashboardError(w, 400, "invalid_archive_verification")
				return
			}
			result, err = h.Store.CreateArchiveReconcile(ctx, site, dataset, request, u.Username)
		} else {
			writeDashboardError(w, 405, "method_not_allowed")
			return
		}
	case "seal":
		if r.Method == http.MethodGet {
			var items []ac.SealTaskItem
			items, err = h.Store.ListArchiveSeals(ctx, site, dataset)
			result = map[string]any{"items": items, "archive_billing": false, "evidence_authority": "archive_readonly_catalog"}
		} else if r.Method == http.MethodPost {
			var request ac.SealRequest
			if !archiveBackfillBody(w, r, &request) || !validArchiveSealRequest(&request) {
				writeDashboardError(w, 400, "invalid_archive_seal")
				return
			}
			result, err = h.Store.CreateArchiveSeal(ctx, site, dataset, request, u.Username)
		} else {
			writeDashboardError(w, 405, "method_not_allowed")
			return
		}
	case "verify_retry", "seal_retry":
		if r.Method != http.MethodPost {
			writeDashboardError(w, 405, "method_not_allowed")
			return
		}
		task := r.PathValue("task")
		if _, e := af.IDBytes(task); e != nil {
			writeDashboardError(w, 400, "invalid_archive_task")
			return
		}
		// There is no force or freeze-bypass parameter. Even retry requests
		// with a body must be empty objects, preventing misleading UI controls.
		if r.Body != nil && r.ContentLength != 0 {
			var body struct{}
			if !archiveBackfillBody(w, r, &body) {
				writeDashboardError(w, 400, "invalid_archive_retry")
				return
			}
		}
		if h.Operation == "verify_retry" {
			result, err = h.Store.RetryArchiveReconcile(ctx, site, dataset, task, u.Username)
		} else {
			result, err = h.Store.RetryArchiveSeal(ctx, site, dataset, task, u.Username)
		}
	default:
		writeDashboardError(w, 404, "not_found")
		return
	}
	if err != nil {
		archiveFoundationError(w, err)
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}

func validArchiveVerificationRequest(request ac.ReconcileRequest) bool {
	if _, err := af.IDBytes(request.RequestID); err != nil || request.Assurance.Validate() != nil {
		return false
	}
	_, end, err := af.DateBounds(request.Date)
	return err == nil && request.Assurance.StableBeforeUnix >= end
}

func validArchiveSealRequest(request *ac.SealRequest) bool {
	if _, err := af.IDBytes(request.RequestID); err != nil || len(request.Dates) == 0 || len(request.Dates) > 31 {
		return false
	}
	sort.Strings(request.Dates)
	for i, date := range request.Dates {
		if _, _, err := af.DateBounds(date); err != nil || (i > 0 && date == request.Dates[i-1]) {
			return false
		}
	}
	return true
}
