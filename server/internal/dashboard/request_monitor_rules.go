package dashboard

import (
	"controltower/server/internal/auth"
	"controltower/server/internal/storage"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func (h Handler) HandleRequestMonitorRules(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	u, ok := auth.CurrentUser(r)
	if !ok || (r.Method == http.MethodGet && !auth.HasPermission(u, "monitor.requests") && !auth.HasPermission(u, "settings.manage")) || (r.Method != http.MethodGet && !auth.HasPermission(u, "settings.manage")) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	store, ok := h.metricSource.(storage.RequestMonitorRulesStore)
	if !ok {
		writeDashboardError(w, 503, "channel_rules_unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		c, err := store.LoadRequestMonitorRules(r.Context())
		if err != nil {
			writeDashboardError(w, 503, "channel_rules_unavailable")
			return
		}
		writeDashboardJSON(w, 200, c)
	case http.MethodPut:
		var c storage.RequestMonitorRules
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&c); err != nil || !c.Valid() {
			writeDashboardError(w, 400, "invalid_channel_rules")
			return
		}
		if decoder.Decode(new(any)) != io.EOF {
			writeDashboardError(w, 400, "invalid_channel_rules")
			return
		}
		ids, err := h.instanceIDsForRequest("", c.SiteID)
		if err != nil {
			writeDashboardError(w, 503, "channel_rules_unavailable")
			return
		}
		if len(ids) == 0 {
			writeDashboardError(w, 400, "invalid_channel_site")
			return
		}
		if err := store.SaveRequestMonitorRules(r.Context(), c, auth.Actor(r)); err != nil {
			if errors.Is(err, storage.ErrRequestRulesConflict) {
				writeDashboardError(w, 409, "channel_rules_conflict")
			} else {
				writeDashboardError(w, 503, "channel_rules_save_failed")
			}
			return
		}
		c.Version++
		writeDashboardJSON(w, 200, c)
	default:
		writeDashboardError(w, 405, "method_not_allowed")
	}
}
