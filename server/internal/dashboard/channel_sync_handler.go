package dashboard

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/channelupdates"
	"controltower/server/internal/tuning"
)

func (h Handler) HandleRefreshTuningChannels(w http.ResponseWriter, r *http.Request) {
	site := tuningSiteID(r)
	if site == "" {
		writeDashboardError(w, 400, "site_id_required")
		return
	}
	store, ok := h.tuningStore.(interface {
		RefreshChannels(context.Context, string, string) error
	})
	if !ok {
		writeDashboardError(w, 501, "channel_refresh_not_supported")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if err := store.RefreshChannels(ctx, site, ctauth.Actor(r)); err != nil {
		if errors.Is(err, tuning.ErrDirectControlNotConfigured) {
			// Agent-managed sites are a valid configuration, not a failure:
			// the page keeps the Agent-collected snapshot and says so only
			// when the operator explicitly asks for a refresh.
			writeDashboardJSON(w, 409, map[string]any{"error": "direct_control_not_configured", "message": "该站点未配置 new-api 直连，渠道信息由 Agent 定期采集"})
			return
		}
		log.Printf("channel refresh site=%s failed: %v", site, err)
		writeDashboardJSON(w, 502, map[string]any{"error": "渠道同步失败，请先确认站点已配置 new-api 直连且连接正常"})
		return
	}
	writeDashboardJSON(w, 200, map[string]any{"synced": true})
}

// Long polling works with session authentication and the existing JSON proxy.
// A persisted write wakes open dashboards immediately, without a snapshot poll.
func (h Handler) HandleTuningChannelChanges(w http.ResponseWriter, r *http.Request) {
	site := tuningSiteID(r)
	if site == "" {
		writeDashboardError(w, 400, "site_id_required")
		return
	}
	revision, changed := channelupdates.Listen(site)
	if r.URL.Query().Get("after") == revision {
		timer := time.NewTimer(25 * time.Second)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		case <-timer.C:
		}
		revision, _ = channelupdates.Listen(site)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeDashboardJSON(w, 200, map[string]any{"revision": revision})
}
