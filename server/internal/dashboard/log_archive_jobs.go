package dashboard

import (
	"context"
	ac "controltower/internal/archivecontrol"
	aj "controltower/internal/archivejob"
	"controltower/server/internal/auth"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

type ArchiveJobsHandler struct{ Store ac.Store }

func (h ArchiveJobsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.CurrentUser(r)
	if !ok || !auth.HasPermission(user, "archive.manage") {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		site := r.URL.Query().Get("site_id")
		if site == "" || len(site) > 64 {
			writeDashboardError(w, 400, "site_required")
			return
		}
		items, err := h.Store.ListLogArchives(r.Context(), site)
		if err != nil {
			writeDashboardError(w, 500, "archive_unavailable")
			return
		}
		month := r.URL.Query().Get("month")
		if month == "" {
			month = time.Now().In(time.FixedZone("Beijing", 28800)).Format("2006-01")
		}
		if _, err = time.Parse("2006-01", month); err != nil {
			writeDashboardError(w, 400, "invalid_month")
			return
		}
		days := []aj.Day{}
		if store, ok := h.Store.(interface {
			ListJobDays(context.Context, string, string) ([]aj.Day, error)
		}); ok {
			days, err = store.ListJobDays(r.Context(), site, month)
			if err != nil {
				writeDashboardError(w, 500, "archive_days_unavailable")
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "days": days, "month": month, "protocol": aj.Protocol})
		return
	}
	var c ac.Config
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil || c.Tasks == nil || !c.Validate() {
		writeDashboardError(w, 400, "invalid_two_task_config")
		return
	}
	err := h.Store.UpdateLogArchive(r.Context(), r.PathValue("id"), c, user.Username)
	if errors.Is(err, ac.ErrConflict) {
		writeDashboardError(w, 409, "archive_config_conflict")
		return
	}
	if err != nil {
		writeDashboardError(w, 500, "archive_save_failed")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"saved": true})
}
