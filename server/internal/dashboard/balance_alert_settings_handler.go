package dashboard

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"controltower/server/internal/auditmeta"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/storage"
)

type BalanceAlertSettingsHandler struct{ Store BalanceAlertSettingsStore }

func (h BalanceAlertSettingsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		writeDashboardError(w, 500, "balance_alert_settings_not_configured")
		return
	}
	site := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if site == "" {
		writeDashboardError(w, 400, "instance_id_required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := h.Store.ListBalanceAlertUserSettings(r.Context(), site)
		if err != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		out := make([]storage.BalanceAlertUserSetting, 0, len(items))
		for _, v := range items {
			out = append(out, v)
		}
		writeDashboardJSON(w, 200, map[string]any{"items": out})
	case http.MethodPut:
		if user, ok := ctauth.CurrentUser(r); ok && user.Role != "admin" {
			writeDashboardError(w, 403, "forbidden")
			return
		}
		var req struct {
			UserID  int64 `json:"user_id"`
			Enabled bool  `json:"enabled"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.UserID <= 0 {
			writeDashboardError(w, 400, "invalid_request")
			return
		}
		v := storage.BalanceAlertUserSetting{InstanceID: site, UserID: req.UserID, Enabled: req.Enabled, UpdatedAt: time.Now().UTC(), UpdatedBy: ctauth.Actor(r)}
		current, err := h.Store.ListBalanceAlertUserSettings(r.Context(), site)
		if err != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		before, existed := current[req.UserID]
		if err := h.Store.PutBalanceAlertUserSetting(r.Context(), v); err != nil {
			writeDashboardError(w, 500, "save_failed")
			return
		}
		if auditStore, ok := any(h.Store).(interface {
			InsertOperationAudit(storage.OperationAudit) error
		}); ok {
			beforeValue := any(map[string]any{})
			if existed {
				beforeValue = before
			}
			beforeJSON, _ := json.Marshal(beforeValue)
			afterJSON, _ := json.Marshal(v)
			var raw [16]byte
			if _, err := rand.Read(raw[:]); err != nil {
				writeDashboardError(w, 500, "audit_failed")
				return
			}
			audit := storage.OperationAudit{ID: "balance-user-" + hex.EncodeToString(raw[:]), InstanceID: site, OperationType: "settings.balance_alert_user_update", TargetType: "balance_alert_user", TargetID: strconv.FormatInt(req.UserID, 10), ActorID: ctauth.Actor(r), SourceComponent: "system", BeforeSummary: string(beforeJSON), AfterSummary: string(afterJSON), Status: "succeeded", CreatedAt: v.UpdatedAt, UpdatedAt: v.UpdatedAt}
			auditmeta.Enrich(r, &audit)
			if err := auditStore.InsertOperationAudit(audit); err != nil {
				writeDashboardError(w, 500, "audit_failed")
				return
			}
			auditmeta.MarkSemanticAudit(r)
		}
		writeDashboardJSON(w, 200, v)
	default:
		writeDashboardError(w, 405, "method_not_allowed")
	}
}
