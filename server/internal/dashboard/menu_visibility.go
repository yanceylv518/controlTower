package dashboard

import (
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/storage"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type MenuVisibilityStore interface {
	MenuVisibility() (string, error)
	SaveMenuVisibility(string, string, time.Time) error
	InsertOperationAudit(storage.OperationAudit) error
}

// These are sidebar entry IDs, not access permissions. Missing entries are visible.
var menuPaths = map[string]bool{
	"/": true, "/customers": true, "/channels": true, "/models": true, "/runtime": true,
	"/usage": true, "/readonly-users": true, "/readonly-logs": true, "/container-logs": true,
	"/billing": true, "/billing/channels": true, "/billing/tasks": true, "/billing/discounts": true,
	"/tuning": true, "/alerts": true, "/notifications": true, "/instances": true, "/log-archives": true,
	"/access-users": true, "/models/manage": true, "/billing/upstreams": true, "/settings": true, "/audits": true,
}

type MenuVisibilityHandler struct{ Store MenuVisibilityStore }

func (h MenuVisibilityHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	if r.Method == http.MethodPut {
		// The dashboard bearer token is trusted; sessions must own settings.manage.
		if user, ok := ctauth.CurrentUser(r); ok && !ctauth.HasPermission(user, "settings.manage") {
			writeDashboardError(w, 403, "forbidden")
			return
		}
	}
	raw, err := h.Store.MenuVisibility()
	if err != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	values := map[string]bool{}
	if err := json.Unmarshal([]byte(raw), &values); err != nil || values == nil {
		writeDashboardError(w, 500, "invalid_stored_menu_visibility")
		return
	}
	if r.Method == http.MethodPut {
		var req struct {
			Items map[string]*bool `json:"items"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil || req.Items == nil {
			writeDashboardError(w, 400, "invalid_menu_visibility")
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			writeDashboardError(w, 400, "invalid_json")
			return
		}
		next := map[string]bool{}
		for path, visible := range req.Items {
			if !menuPaths[path] || visible == nil {
				writeDashboardError(w, 400, "invalid_menu_visibility")
				return
			}
			next[path] = *visible
		}
		encoded, _ := json.Marshal(next)
		actor := ctauth.Actor(r)
		if err := h.Store.SaveMenuVisibility(string(encoded), actor, time.Now().UTC()); err != nil {
			writeDashboardError(w, 500, "update_failed")
			return
		}
		id := make([]byte, 16)
		_, _ = rand.Read(id)
		if err := h.Store.InsertOperationAudit(storage.OperationAudit{ID: hex.EncodeToString(id), OperationType: "menu_visibility.update", TargetType: "system_settings", TargetID: "global", ActorID: actor, BeforeSummary: raw, AfterSummary: string(encoded), Status: "succeeded", CreatedAt: time.Now().UTC()}); err != nil {
			writeDashboardError(w, 500, "audit_failed")
			return
		}
		values = next
	}
	writeDashboardJSON(w, 200, map[string]any{"items": values})
}
