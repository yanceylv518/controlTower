package dashboard

import (
	ac "controltower/internal/archivecontrol"
	"controltower/server/internal/auth"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

type LogArchiveHandler struct{ Store ac.Store }

func (h LogArchiveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.CurrentUser(r)
	if !ok || !auth.HasPermission(u, "archive.manage") {
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
			month = time.Now().In(time.FixedZone("UTC+08:00", 28800)).Format("2006-01")
		}
		if _, err := time.Parse("2006-01", month); err != nil {
			writeDashboardError(w, 400, "invalid_archive_month")
			return
		}
		if len(items) > 0 {
			items[0].Days, err = h.Store.ListLogArchiveDays(r.Context(), site, month)
			if err != nil {
				writeDashboardError(w, 500, "archive_days_unavailable")
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		return
	}
	var c ac.Config
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || !c.Validate() {
		writeDashboardError(w, 400, "invalid_archive_config")
		return
	}
	if err := h.Store.UpdateLogArchive(r.Context(), r.PathValue("id"), c, u.Username); err != nil {
		if errors.Is(err, ac.ErrConflict) {
			writeDashboardError(w, 409, "archive_config_conflict")
		} else {
			writeDashboardError(w, 500, "archive_save_failed")
		}
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"saved": true})
}
