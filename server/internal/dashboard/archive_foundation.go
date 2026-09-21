package dashboard

import (
	"context"
	ac "controltower/internal/archivecontract"
	"controltower/server/internal/auth"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

type ArchiveFoundationStore interface {
	RegisterArchiveDataset(context.Context, ac.Registration, string) error
	GetArchiveDataset(context.Context, string, string) (ac.Dataset, error)
	ApplyArchiveCatalog(context.Context, ac.CatalogSnapshot) (bool, error)
}
type ArchiveCatalogReader interface {
	ReadCatalog(context.Context, ac.Registration) (ac.CatalogSnapshot, error)
}
type ArchiveFoundationHandler struct {
	Store  ArchiveFoundationStore
	Reader ArchiveCatalogReader
}

func (h ArchiveFoundationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.CurrentUser(r)
	if !ok || !auth.HasPermission(u, "archive.manage") {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if r.Method == http.MethodPost && r.PathValue("dataset") == "" {
		var registration ac.Registration
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		d.DisallowUnknownFields()
		if d.Decode(&registration) != nil || d.Decode(new(any)) != io.EOF || registration.Validate() != nil {
			writeDashboardError(w, 400, "invalid_archive_registration")
			return
		}
		if h.Reader == nil {
			writeDashboardError(w, 503, "archive_readonly_unavailable")
			return
		}
		// Verify the actual target from Server before making its binding active.
		if _, err := h.Reader.ReadCatalog(ctx, registration); err != nil {
			archiveFoundationError(w, err)
			return
		}
		if err := h.Store.RegisterArchiveDataset(ctx, registration, u.Username); err != nil {
			archiveFoundationError(w, err)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"registered": true, "state": "paused", "archive_billing": false})
		return
	}
	site, id := r.URL.Query().Get("site_id"), r.PathValue("dataset")
	if site == "" || len(site) > 64 {
		writeDashboardError(w, 400, "site_required")
		return
	}
	if _, err := ac.IDBytes(id); err != nil {
		writeDashboardError(w, 400, "invalid_dataset_id")
		return
	}
	dataset, err := h.Store.GetArchiveDataset(ctx, site, id)
	if err != nil {
		archiveFoundationError(w, err)
		return
	}
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(dataset)
		return
	}
	if r.Method != http.MethodPost {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	if h.Reader == nil {
		writeDashboardError(w, 503, "archive_readonly_unavailable")
		return
	}
	snapshot, err := h.Reader.ReadCatalog(ctx, dataset.Registration)
	if err != nil {
		archiveFoundationError(w, err)
		return
	}
	applied, err := h.Store.ApplyArchiveCatalog(ctx, snapshot)
	if err != nil {
		archiveFoundationError(w, err)
		return
	}
	_ = json.NewEncoder(w).Encode(struct {
		Applied        bool   `json:"applied"`
		Revision       uint64 `json:"catalog_revision,string"`
		ArchiveBilling bool   `json:"archive_billing"`
	}{Applied: applied, Revision: snapshot.CatalogRevision})
}
func archiveFoundationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ac.ErrIdentity):
		writeDashboardError(w, 409, "archive_identity_mismatch")
	case errors.Is(err, ac.ErrConflict):
		writeDashboardError(w, 409, "archive_foundation_conflict")
	case errors.Is(err, ac.ErrUnsupported):
		writeDashboardError(w, 409, "archive_version_unsupported")
	case errors.Is(err, ac.ErrNotFound):
		writeDashboardError(w, 404, "archive_dataset_not_found")
	default:
		writeDashboardError(w, 503, "archive_foundation_unavailable")
	}
}
