package dashboard

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/settings"
	"controltower/server/internal/storage"
)

type SettingsStore interface {
	storage.SystemSettingStore
	InsertOperationAudit(storage.OperationAudit) error
}

type SettingsHandler struct {
	Store    SettingsStore
	Provider *settings.Provider
}
type settingsUpdateRequest struct {
	Values map[string]string `json:"values"`
}

func (h SettingsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil || h.Provider == nil {
		writeDashboardError(w, 500, "settings_not_configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.respond(w)
	case http.MethodPut:
		var req settingsUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeDashboardError(w, 400, "invalid_json")
			return
		}
		if fieldErrors := settings.Validate(req.Values); len(fieldErrors) > 0 {
			writeDashboardJSON(w, 400, map[string]any{"error": "invalid_settings", "fields": fieldErrors})
			return
		}
		before, _ := h.Provider.Items()
		actor := ctauth.Actor(r)
		if actor == "" {
			actor = "unknown"
		}
		if err := h.Store.ReplaceSystemSettings(req.Values, actor, time.Now().UTC()); err != nil {
			writeDashboardError(w, 500, "update_failed")
			return
		}
		h.Provider.Invalidate()
		after, _ := h.Provider.Items()
		if err := h.audit(actor, before, after); err != nil {
			writeDashboardError(w, 500, "audit_failed")
			return
		}
		h.respond(w)
	default:
		writeDashboardError(w, 405, "method_not_allowed")
	}
}

func (h SettingsHandler) respond(w http.ResponseWriter) {
	items, err := h.Provider.Items()
	if err != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	writeDashboardJSON(w, 200, map[string]any{"items": items})
}
func (h SettingsHandler) audit(actor string, before, after map[string]settings.Item) error {
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return h.Store.InsertOperationAudit(storage.OperationAudit{ID: hex.EncodeToString(raw), OperationType: "settings.update", TargetType: "system_settings", TargetID: "global", ActorID: actor, BeforeSummary: string(b), AfterSummary: string(a), Status: "succeeded", CreatedAt: time.Now().UTC()})
}
