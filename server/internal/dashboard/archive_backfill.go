package dashboard

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
	"controltower/server/internal/auth"
)

type ArchiveBackfillStore interface {
	GetArchiveCoveragePolicy(context.Context, string, string) (ac.ArchiveCoveragePolicy, error)
	UpdateArchiveCoveragePolicy(context.Context, string, string, af.CoveragePolicy, string) (ac.ArchiveCoveragePolicy, error)
	GetArchiveCoverage(context.Context, string, string, string) (ac.ArchiveCoverageMonth, error)
	CreateArchiveBackfill(context.Context, string, string, ac.BackfillRequest, string) (ac.BackfillTaskItem, error)
	ListArchiveBackfills(context.Context, string, string) ([]ac.BackfillTaskItem, error)
	RetryArchiveBackfill(context.Context, string, string, string, string) (ac.BackfillTaskItem, error)
}

type ArchiveBackfillHandler struct {
	Store     ArchiveBackfillStore
	Operation string
}

func (h ArchiveBackfillHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	case "coverage":
		if r.Method != http.MethodGet {
			writeDashboardError(w, 405, "method_not_allowed")
			return
		}
		month := r.URL.Query().Get("month")
		if d, e := time.Parse("2006-01", month); e != nil || d.Format("2006-01") != month {
			writeDashboardError(w, 400, "invalid_archive_month")
			return
		}
		result, err = h.Store.GetArchiveCoverage(ctx, site, dataset, month)
	case "policy":
		if r.Method == http.MethodGet {
			result, err = h.Store.GetArchiveCoveragePolicy(ctx, site, dataset)
		} else if r.Method == http.MethodPut {
			var p af.CoveragePolicy
			if !archiveBackfillBody(w, r, &p) || p.Validate() != nil || p.CoverageFrom == "" || p.Evidence == "" {
				writeDashboardError(w, 400, "invalid_archive_coverage_policy")
				return
			}
			result, err = h.Store.UpdateArchiveCoveragePolicy(ctx, site, dataset, p, u.Username)
		} else {
			writeDashboardError(w, 405, "method_not_allowed")
			return
		}
	case "tasks":
		if r.Method == http.MethodGet {
			var items []ac.BackfillTaskItem
			items, err = h.Store.ListArchiveBackfills(ctx, site, dataset)
			result = map[string]any{"items": items, "archive_billing": false, "day_versions": false}
		} else if r.Method == http.MethodPost {
			var request ac.BackfillRequest
			if !archiveBackfillBody(w, r, &request) {
				writeDashboardError(w, 400, "invalid_archive_backfill")
				return
			}
			if _, e := af.IDBytes(request.RequestID); e != nil {
				writeDashboardError(w, 400, "invalid_archive_backfill")
				return
			}
			if _, _, e := af.DateBounds(request.Date); e != nil {
				writeDashboardError(w, 400, "invalid_archive_backfill")
				return
			}
			result, err = h.Store.CreateArchiveBackfill(ctx, site, dataset, request, u.Username)
		} else {
			writeDashboardError(w, 405, "method_not_allowed")
			return
		}
	case "retry":
		if r.Method != http.MethodPost {
			writeDashboardError(w, 405, "method_not_allowed")
			return
		}
		task := r.PathValue("task")
		if _, e := af.IDBytes(task); e != nil {
			writeDashboardError(w, 400, "invalid_archive_task")
			return
		}
		result, err = h.Store.RetryArchiveBackfill(ctx, site, dataset, task, u.Username)
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

func archiveBackfillBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	return d.Decode(dst) == nil && d.Decode(new(any)) == io.EOF
}
