package dashboard

import (
	"context"
	"controltower/server/internal/archivereader"
	"controltower/server/internal/auth"
	"controltower/server/internal/secrets"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

type ArchiveConnectionHandler struct {
	Store     archivereader.ConnectionStore
	SecretKey string
	Probe     func(context.Context, archivereader.Connection) (string, error)
}

func (h ArchiveConnectionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.CurrentUser(r)
	if !ok || !auth.HasPermission(u, "archive.manage") {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	site := r.URL.Query().Get("site_id")
	if site == "" || len(site) > 64 {
		writeDashboardError(w, 400, "site_required")
		return
	}
	if h.Store == nil {
		writeDashboardError(w, 503, "archive_connection_unavailable")
		return
	}
	exists, err := h.Store.ArchiveSiteExists(r.Context(), site)
	if err != nil {
		writeDashboardError(w, 503, "archive_connection_unavailable")
		return
	}
	if !exists {
		writeDashboardError(w, 404, "site_not_found")
		return
	}
	old, err := h.Store.LoadArchiveConnection(r.Context(), site)
	if err != nil && !errors.Is(err, archivereader.ErrConnectionMissing) {
		writeDashboardError(w, 503, "archive_connection_unavailable")
		return
	}
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(map[string]any{"configured": err == nil, "connection": old})
		return
	}
	if h.SecretKey == "" {
		writeDashboardError(w, 503, "secret_key_not_configured")
		return
	}
	var input struct {
		archivereader.Connection
		Password string `json:"password"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil || dec.Decode(new(any)) != io.EOF || input.Validate() != nil || len(input.Password) > 2048 {
		writeDashboardError(w, 400, "archive_invalid_connection")
		return
	}
	if input.Version != old.Version {
		writeDashboardError(w, 409, "archive_connection_conflict")
		return
	}
	input.EncryptedPassword = old.EncryptedPassword
	if input.Password != "" {
		input.EncryptedPassword, err = secrets.Encrypt(h.SecretKey, input.Password)
		if err != nil {
			writeDashboardError(w, 503, "archive_connection_unavailable")
			return
		}
	}
	if input.EncryptedPassword == "" {
		writeDashboardError(w, 400, "archive_password_required")
		return
	}
	// An existing site binding cannot silently move to another dataset.
	if old.SourceHash != "" {
		input.SourceHash = old.SourceHash
	}
	if r.Method == http.MethodPut && input.SourceHash == "" {
		writeDashboardError(w, 400, "archive_test_required")
		return
	}
	probe := h.Probe
	if probe == nil {
		probe = archivereader.Reader{SecretKey: h.SecretKey}.ProbeConnection
	}
	hash, err := probe(r.Context(), input.Connection)
	if err != nil {
		code := "archive_connection_test_failed"
		if errors.Is(err, archivereader.ErrIdentity) {
			code = "archive_identity_or_schema_mismatch"
		}
		if errors.Is(err, archivereader.ErrPermissions) {
			code = "archive_readonly_permissions_required"
		}
		writeDashboardError(w, 422, code)
		return
	}
	input.SourceHash = hash
	input.TestedAt = time.Now().UTC().Format(time.RFC3339)
	input.PasswordSet = true
	if r.Method == http.MethodPut {
		if err = h.Store.SaveArchiveConnection(r.Context(), site, input.Connection, u.Username); err != nil {
			if errors.Is(err, archivereader.ErrConnectionConflict) {
				writeDashboardError(w, 409, "archive_connection_conflict")
			} else {
				writeDashboardError(w, 503, "archive_connection_save_failed")
			}
			return
		}
		input.Version++
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"connection": input.Connection, "saved": r.Method == http.MethodPut})
}
