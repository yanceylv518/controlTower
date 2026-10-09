package dashboard

import (
	"context"
	"controltower/server/internal/alblog"
	"controltower/server/internal/auth"
	"controltower/server/internal/secrets"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// ALBAccessLogHandler is separate from CT system logs and general settings.
type ALBAccessLogHandler struct {
	Store     alblog.Store
	SecretKey string
	Probe     func(context.Context, alblog.Config) alblog.Result
	Slots     chan struct{}
}

func (h *ALBAccessLogHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	u, ok := auth.CurrentUser(r)
	if !ok || !auth.HasPermission(u, "settings.manage") {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	if h.Store == nil {
		writeDashboardError(w, 503, "alb_config_unavailable")
		return
	}
	old, err := h.Store.LoadALBLogConfig(r.Context())
	if err != nil && !errors.Is(err, alblog.ErrMissing) {
		writeDashboardError(w, 503, "alb_config_unavailable")
		return
	}
	if r.Method == http.MethodGet {
		writeDashboardJSON(w, 200, map[string]any{"configured": err == nil, "connection": old})
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	if h.SecretKey == "" {
		writeDashboardError(w, 503, "secret_key_not_configured")
		return
	}
	var input struct {
		Endpoint        string `json:"endpoint"`
		Project         string `json:"project"`
		Logstore        string `json:"logstore"`
		ALBID           string `json:"alb_id"`
		AccessKeyID     string `json:"access_key_id"`
		AccessKeySecret string `json:"access_key_secret"`
		Version         int64  `json:"version"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil || dec.Decode(new(any)) != io.EOF || len(input.AccessKeySecret) > 2048 {
		writeDashboardError(w, 400, "alb_invalid_config")
		return
	}
	cfg := alblog.Config{Endpoint: input.Endpoint, Project: input.Project, Logstore: input.Logstore, ALBID: input.ALBID, AccessKeyID: input.AccessKeyID, Version: input.Version, SecretCipher: old.SecretCipher}
	if cfg.Normalize() != nil {
		writeDashboardError(w, 400, "alb_invalid_config")
		return
	}
	if cfg.Version != old.Version {
		writeDashboardError(w, 409, "alb_config_conflict")
		return
	}
	// An ID rotation must supply its matching secret; blank only preserves the existing pair.
	if input.AccessKeySecret == "" && cfg.AccessKeyID != old.AccessKeyID {
		writeDashboardError(w, 400, "alb_secret_required")
		return
	}
	if input.AccessKeySecret != "" {
		cfg.SecretCipher, err = secrets.Encrypt(h.SecretKey, input.AccessKeySecret)
		if err != nil {
			writeDashboardError(w, 503, "alb_secret_unavailable")
			return
		}
	}
	if cfg.SecretCipher == "" {
		writeDashboardError(w, 400, "alb_secret_required")
		return
	}
	cfg.SecretSet = true
	if h.Slots != nil {
		select {
		case h.Slots <- struct{}{}:
			defer func() { <-h.Slots }()
		default:
			writeDashboardError(w, 429, "alb_test_busy")
			return
		}
	}
	probe := h.Probe
	if probe == nil {
		probe = alblog.Client{SecretKey: h.SecretKey}.Probe
	}
	result := probe(r.Context(), cfg)
	cfg.LastTest = &result
	// Draft tests never replace a saved connection. Testing an unchanged saved
	// connection persists the result using the same optimistic version guard.
	persisted := r.Method == http.MethodPut || (old.Version > 0 && alblog.SameConnection(old, cfg))
	if persisted {
		if err = h.Store.SaveALBLogConfig(r.Context(), cfg, u.Username); err != nil {
			if errors.Is(err, alblog.ErrConflict) {
				writeDashboardError(w, 409, "alb_config_conflict")
			} else {
				writeDashboardError(w, 503, "alb_config_save_failed")
			}
			return
		}
		cfg.Version++
	}
	writeDashboardJSON(w, 200, map[string]any{"connection": cfg, "saved": r.Method == http.MethodPut, "persisted": persisted})
}
