package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
)

type BillingUpstreamConfigStore interface {
	ListBillingUpstreams(context.Context, string) ([]billing.Upstream, error)
	PutBillingUpstream(context.Context, billing.Upstream) (billing.Upstream, error)
	DeleteBillingUpstream(context.Context, string, int64) error
}

type BillingUpstreamConfigSource interface {
	CurrentChannels(context.Context, string) ([]billing.ConfiguredChannel, error)
}

type BillingUpstreamConfigHandler struct {
	Store  BillingUpstreamConfigStore
	Source BillingUpstreamConfigSource
}

func (h BillingUpstreamConfigHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !billingAdminAllowed(r) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	switch r.Method {
	case http.MethodGet:
		site := strings.TrimSpace(r.URL.Query().Get("instance_id"))
		if site == "" {
			writeDashboardError(w, 400, "instance_id_required")
			return
		}
		if h.Source == nil || !billingReadonlyAvailable(h.Store, site) {
			writeDashboardError(w, http.StatusConflict, "readonly_source_unavailable")
			return
		}
		channels, err := h.Source.CurrentChannels(r.Context(), site)
		if err != nil {
			writeDashboardError(w, http.StatusBadGateway, "readonly_channels_query_failed")
			return
		}
		items, err := h.Store.ListBillingUpstreams(r.Context(), site)
		if err != nil {
			writeDashboardError(w, 500, "billing_upstreams_query_failed")
			return
		}
		writeDashboardJSON(w, 200, map[string]any{"items": items, "channels": channels})
	case http.MethodPost, http.MethodPut:
		var item billing.Upstream
		if json.NewDecoder(r.Body).Decode(&item) != nil || strings.TrimSpace(item.InstanceID) == "" || strings.TrimSpace(item.Name) == "" {
			writeDashboardError(w, 400, "invalid_upstream")
			return
		}
		if r.Method == http.MethodPost {
			item.ID = 0
		} else if item.ID <= 0 {
			writeDashboardError(w, 400, "upstream_id_required")
			return
		}
		seen := map[int64]bool{}
		for i := range item.Channels {
			if item.Channels[i].ChannelID <= 0 || seen[item.Channels[i].ChannelID] {
				writeDashboardError(w, 400, "invalid_upstream_channels")
				return
			}
			seen[item.Channels[i].ChannelID] = true
		}
		item.Name = strings.TrimSpace(item.Name)
		item.Remark = strings.TrimSpace(item.Remark)
		if h.Source == nil || !billingReadonlyAvailable(h.Store, item.InstanceID) {
			writeDashboardError(w, http.StatusConflict, "readonly_source_unavailable")
			return
		}
		configured, sourceErr := h.Source.CurrentChannels(r.Context(), item.InstanceID)
		if sourceErr != nil {
			writeDashboardError(w, http.StatusBadGateway, "readonly_channels_query_failed")
			return
		}
		current := make(map[int64]billing.ConfiguredChannel, len(configured))
		for _, channel := range configured {
			current[channel.ChannelID] = channel
		}
		for i := range item.Channels {
			channel, ok := current[item.Channels[i].ChannelID]
			if !ok {
				writeDashboardError(w, http.StatusConflict, "channel_not_in_readonly_source")
				return
			}
			item.Channels[i].ChannelName = channel.ChannelName
		}
		item.UpdatedBy = ctauth.Actor(r)
		if item.UpdatedBy == "" {
			item.UpdatedBy = "legacy-admin"
		}
		saved, err := h.Store.PutBillingUpstream(r.Context(), item)
		if err != nil {
			if err == sql.ErrNoRows {
				writeDashboardError(w, 404, "upstream_not_found")
			} else {
				writeDashboardError(w, 409, "upstream_save_failed")
			}
			return
		}
		writeDashboardJSON(w, 200, saved)
	case http.MethodDelete:
		site := strings.TrimSpace(r.URL.Query().Get("instance_id"))
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if site == "" || id <= 0 {
			writeDashboardError(w, 400, "invalid_upstream")
			return
		}
		if err := h.Store.DeleteBillingUpstream(r.Context(), site, id); err != nil {
			if err == sql.ErrNoRows {
				writeDashboardError(w, 404, "upstream_not_found")
			} else {
				writeDashboardError(w, 500, "upstream_delete_failed")
			}
			return
		}
		writeDashboardJSON(w, 200, map[string]any{"deleted": true})
	default:
		writeDashboardError(w, 405, "method_not_allowed")
	}
}
