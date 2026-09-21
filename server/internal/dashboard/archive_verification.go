package dashboard

import (
	"context"
	af "controltower/internal/archivecontract"
	"controltower/server/internal/auth"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type ArchiveVerificationReader interface {
	ReadDayVerification(context.Context, af.Registration, string, string, int64) (af.DayVerification, error)
}
type ArchiveVerificationHandler struct {
	Store  ArchiveFoundationStore
	Reader ArchiveVerificationReader
}

func (h ArchiveVerificationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.CurrentUser(r)
	if !ok || !auth.HasPermission(user, "archive.manage") {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	if r.Method != http.MethodGet {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	site, id, date := r.URL.Query().Get("site_id"), r.PathValue("dataset"), r.PathValue("date")
	run := r.URL.Query().Get("run_id")
	if site == "" || len(site) > 64 {
		writeDashboardError(w, 400, "site_required")
		return
	}
	if _, err := af.IDBytes(id); err != nil {
		writeDashboardError(w, 400, "invalid_dataset_id")
		return
	}
	if _, _, err := af.DateBounds(date); err != nil {
		writeDashboardError(w, 400, "invalid_date")
		return
	}
	if run != "" {
		if _, err := af.IDBytes(run); err != nil {
			writeDashboardError(w, 400, "invalid_run_id")
			return
		}
	}
	var after int64
	var err error
	if raw := r.URL.Query().Get("after_id"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || after < 0 {
			writeDashboardError(w, 400, "invalid_cursor")
			return
		}
	}
	// A continuation is anchored to the original run. Selecting "latest"
	// again could silently skip issues if another run starts between pages.
	if after > 0 && run == "" {
		writeDashboardError(w, 400, "run_required_for_cursor")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	dataset, err := h.Store.GetArchiveDataset(ctx, site, id)
	if err != nil {
		archiveFoundationError(w, err)
		return
	}
	if h.Reader == nil {
		writeDashboardError(w, 503, "archive_readonly_unavailable")
		return
	}
	result, err := h.Reader.ReadDayVerification(ctx, dataset.Registration, date, run, after)
	if err != nil {
		archiveFoundationError(w, err)
		return
	}
	if !result.Identity.Equal(dataset.Identity) || result.Date != date || (run != "" && result.SelectedRunID != run) {
		archiveFoundationError(w, af.ErrIdentity)
		return
	}
	result.ArchiveBilling = false
	_ = json.NewEncoder(w).Encode(result)
}
