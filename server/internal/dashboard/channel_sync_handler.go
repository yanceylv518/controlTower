package dashboard

import (
	"context"
	"log"
	"net/http"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/channelupdates"
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
		log.Printf("channel refresh site=%s failed: %v", site, err)
		writeDashboardJSON(w, 502, map[string]any{"error": "渠道同步失败，请先确认站点已配置 new-api 直连且连接正常"})
		return
	}
	writeDashboardJSON(w, 200, map[string]any{"synced": true})
}

// Long polling works with session authentication and the existing JSON proxy.
// A persisted write wakes open dashboards immediately, without a snapshot poll.
func (h Handler) HandleTuningChannelChanges(w http.ResponseWriter, r *http.Request) {
	if tuningSiteID(r) == "" {
		writeDashboardError(w, 400, "site_id_required")
		return
	}
	revision, changed := channelupdates.Listen()
	if r.URL.Query().Get("after") == revision {
		timer := time.NewTimer(25 * time.Second)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		case <-timer.C:
		}
		revision, _ = channelupdates.Listen()
	}
	w.Header().Set("Cache-Control", "no-store")
	writeDashboardJSON(w, 200, map[string]any{"revision": revision})
}
