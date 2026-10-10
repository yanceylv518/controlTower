package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

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

func listUpstreamConfig(ctx context.Context, store BillingUpstreamConfigStore, site string) ([]billing.Upstream, error) {
	if local, ok := store.(interface {
		ListBillingUpstreamsConfig(context.Context, string) ([]billing.Upstream, error)
	}); ok {
		return local.ListBillingUpstreamsConfig(ctx, site)
	}
	return store.ListBillingUpstreams(ctx, site)
}
func (h BillingUpstreamConfigHandler) source(ctx context.Context, site string) ([]billing.ConfiguredChannel, string) {
	if h.Source == nil || !billingReadonlyAvailable(h.Store, site) {
		return nil, "readonly_source_unavailable"
	}
	queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	channels, err := h.Source.CurrentChannels(queryCtx, site)
	if err != nil {
		return nil, "readonly_channels_query_failed"
	}
	return channels, ""
}
func (h BillingUpstreamConfigHandler) configResponse(ctx context.Context, site string, items []billing.Upstream, source []billing.ConfiguredChannel, sourceError string) (map[string]any, error) {
	directory := map[int64]billing.ConfiguredChannel{}
	for _, c := range source {
		directory[c.ChannelID] = c
	}
	for _, up := range items {
		for _, c := range up.Channels {
			if _, ok := directory[c.ChannelID]; !ok {
				directory[c.ChannelID] = billing.ConfiguredChannel{ChannelID: c.ChannelID, ChannelName: c.ChannelName, SourceMissing: true}
			}
		}
	}
	if store, ok := h.Store.(interface {
		BillingUpstreamChannelExclusions(context.Context, string) ([]billing.ConfiguredChannel, error)
	}); ok {
		excluded, err := store.BillingUpstreamChannelExclusions(ctx, site)
		if err != nil {
			return nil, err
		}
		for _, c := range excluded {
			if current, ok := directory[c.ChannelID]; ok {
				current.AutoExcluded = true
				directory[c.ChannelID] = current
			} else {
				c.SourceMissing = true
				directory[c.ChannelID] = c
			}
		}
	}
	channels := []billing.ConfiguredChannel{}
	for _, c := range directory {
		channels = append(channels, c)
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].ChannelID < channels[j].ChannelID })
	response := map[string]any{"items": items, "channels": channels, "source_available": sourceError == ""}
	if sourceError != "" {
		response["sync_error"] = sourceError
	}
	if store, ok := h.Store.(interface {
		BillingUpstreamSyncedAt(context.Context, string) (*time.Time, error)
	}); ok {
		at, err := store.BillingUpstreamSyncedAt(ctx, site)
		if err != nil {
			return nil, err
		}
		if at != nil {
			response["synced_at"] = at
		}
	}
	return response, nil
}
func upstreamWriteError(w http.ResponseWriter, err error) {
	status, code := 409, "upstream_save_failed"
	switch {
	case errors.Is(err, sql.ErrNoRows):
		status, code = 404, "upstream_not_found"
	case errors.Is(err, billing.ErrUpstreamRevisionConflict):
		code = "upstream_revision_conflict"
	case errors.Is(err, billing.ErrUpstreamPrefixConflict):
		code = "upstream_prefix_conflict"
	case errors.Is(err, billing.ErrUpstreamChannelConflict):
		code = "upstream_channel_conflict"
	case errors.Is(err, billing.ErrUpstreamTransferConflict):
		code = "upstream_transfer_conflict"
	case errors.Is(err, billing.ErrUpstreamInUse):
		code = "upstream_in_use"
	}
	writeDashboardError(w, status, code)
}
func (h BillingUpstreamConfigHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !billingAdminAllowed(r) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	if r.Method == http.MethodPost && r.URL.Query().Get("action") == "sync" {
		h.sync(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		site := strings.TrimSpace(r.URL.Query().Get("instance_id"))
		if site == "" {
			writeDashboardError(w, 400, "instance_id_required")
			return
		}
		items, err := listUpstreamConfig(r.Context(), h.Store, site)
		if err != nil {
			writeDashboardError(w, 500, "billing_upstreams_query_failed")
			return
		}
		channels, sourceError := h.source(r.Context(), site)
		response, err := h.configResponse(r.Context(), site, items, channels, sourceError)
		if err != nil {
			writeDashboardError(w, 500, "billing_upstreams_query_failed")
			return
		}
		writeDashboardJSON(w, 200, response)
	case http.MethodPost, http.MethodPut:
		h.save(w, r)
	case http.MethodDelete:
		site := strings.TrimSpace(r.URL.Query().Get("instance_id"))
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if site == "" || id <= 0 {
			writeDashboardError(w, 400, "invalid_upstream")
			return
		}
		items, err := listUpstreamConfig(r.Context(), h.Store, site)
		if err != nil {
			writeDashboardError(w, 500, "billing_upstreams_query_failed")
			return
		}
		var before any = map[string]any{}
		for _, v := range items {
			if v.ID == id {
				before = v
			}
		}
		if err = h.Store.DeleteBillingUpstream(r.Context(), site, id); err != nil {
			upstreamWriteError(w, err)
			return
		}
		response := map[string]any{"deleted": true}
		if audit, ok := h.Store.(billingAuditStore); ok {
			if err = auditBillingMutation(audit, r, site, "billing.upstream.delete", strconv.FormatInt(id, 10), before, map[string]any{"deleted": true}); err != nil {
				response["sync_error"] = "billing_upstream_audit_failed"
			}
		}
		writeDashboardJSON(w, 200, response)
	default:
		writeDashboardError(w, 405, "method_not_allowed")
	}
}
func (h BillingUpstreamConfigHandler) sync(w http.ResponseWriter, r *http.Request) {
	site := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if site == "" {
		writeDashboardError(w, 400, "instance_id_required")
		return
	}
	var req struct {
		Restore []int64 `json:"restore_auto_channel_ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil && err != io.EOF {
		writeDashboardError(w, 400, "invalid_request")
		return
	}
	source, sourceError := h.source(r.Context(), site)
	if sourceError != "" {
		writeDashboardError(w, 502, sourceError)
		return
	}
	syncer, ok := h.Store.(interface {
		SyncBillingUpstreamChannelsWithRestore(context.Context, string, []billing.ConfiguredChannel, []int64, string) error
	})
	if !ok {
		writeDashboardError(w, 500, "upstream_sync_unavailable")
		return
	}
	actor := ctauth.Actor(r)
	if actor == "" {
		actor = "legacy-admin"
	}
	if err := syncer.SyncBillingUpstreamChannelsWithRestore(r.Context(), site, source, req.Restore, actor); err != nil {
		upstreamWriteError(w, err)
		return
	}
	items, err := listUpstreamConfig(r.Context(), h.Store, site)
	if err != nil {
		writeDashboardError(w, 500, "billing_upstreams_query_failed")
		return
	}
	response, err := h.configResponse(r.Context(), site, items, source, "")
	if err != nil {
		writeDashboardError(w, 500, "billing_upstreams_query_failed")
		return
	}
	if audit, ok := h.Store.(billingAuditStore); ok {
		if err = auditBillingMutation(audit, r, site, "billing.upstream.sync", site, map[string]any{}, map[string]any{"restored_channel_ids": req.Restore}); err != nil {
			response["sync_error"] = "billing_upstream_audit_failed"
		}
	}
	writeDashboardJSON(w, 200, response)
}
func (h BillingUpstreamConfigHandler) save(w http.ResponseWriter, r *http.Request) {
	var item billing.Upstream
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&item) != nil {
		writeDashboardError(w, 400, "invalid_upstream")
		return
	}
	item.InstanceID = strings.TrimSpace(item.InstanceID)
	item.Name = strings.TrimSpace(item.Name)
	item.Remark = strings.TrimSpace(item.Remark)
	item.URL = strings.TrimSpace(item.URL)
	if item.InstanceID == "" || len([]rune(item.InstanceID)) > 64 || item.Name == "" || len([]rune(item.Name)) > 128 || len([]rune(item.Remark)) > 500 {
		writeDashboardError(w, 400, "invalid_upstream")
		return
	}
	if r.Method == http.MethodPost {
		item.ID = 0
	} else if item.ID <= 0 {
		writeDashboardError(w, 400, "upstream_id_required")
		return
	}
	if item.ChannelPrefixes != nil {
		if len(item.ChannelPrefixes) > 50 {
			writeDashboardError(w, 400, "invalid_channel_prefix")
			return
		}
		clean := []string{}
		seen := map[string]bool{}
		for _, v := range item.ChannelPrefixes {
			p := strings.TrimRight(strings.TrimSpace(v), "_")
			if p == "" || len([]rune(p)) > 128 {
				writeDashboardError(w, 400, "invalid_channel_prefix")
				return
			}
			if !seen[p] {
				clean = append(clean, p)
				seen[p] = true
			}
		}
		item.ChannelPrefixes = clean
	}
	for i := range item.PrefixTransfers {
		item.PrefixTransfers[i].Prefix = strings.TrimRight(strings.TrimSpace(item.PrefixTransfers[i].Prefix), "_")
	}
	if item.URL != "" {
		normalized, err := billing.NormalizeUpstreamURL(item.URL)
		if err != nil {
			writeDashboardError(w, 400, "invalid_upstream_url")
			return
		}
		item.URL = normalized
	}
	existing, err := listUpstreamConfig(r.Context(), h.Store, item.InstanceID)
	if err != nil {
		writeDashboardError(w, 500, "billing_upstreams_query_failed")
		return
	}
	var before *billing.Upstream
	owners := map[int64]int64{}
	known := map[int64]billing.ConfiguredChannel{}
	for i := range existing {
		up := &existing[i]
		if up.ID == item.ID {
			before = up
		}
		for _, c := range up.Channels {
			owners[c.ChannelID] = up.ID
			known[c.ChannelID] = billing.ConfiguredChannel{ChannelID: c.ChannelID, ChannelName: c.ChannelName, SourceMissing: true}
		}
	}
	if item.ID > 0 && before == nil {
		writeDashboardError(w, 404, "upstream_not_found")
		return
	}
	if before != nil && before.Revision > 0 && before.Revision != item.Revision {
		upstreamWriteError(w, billing.ErrUpstreamRevisionConflict)
		return
	}
	source, sourceError := h.source(r.Context(), item.InstanceID)
	for _, c := range source {
		known[c.ChannelID] = c
	}
	if ex, ok := h.Store.(interface {
		BillingUpstreamChannelExclusions(context.Context, string) ([]billing.ConfiguredChannel, error)
	}); ok {
		excluded, e := ex.BillingUpstreamChannelExclusions(r.Context(), item.InstanceID)
		if e != nil {
			writeDashboardError(w, 500, "billing_upstreams_query_failed")
			return
		}
		for _, c := range excluded {
			if _, ok = known[c.ChannelID]; !ok {
				known[c.ChannelID] = c
			}
		}
	}
	item.Channels = []billing.UpstreamChannel{}
	item.URLs = []string{}
	if before != nil {
		item.Channels = append(item.Channels, before.Channels...)
		item.URLs = append(item.URLs, before.URLs...)
	}
	if item.URL != "" {
		item.URLs = append(item.URLs, item.URL)
	}
	removed := map[int64]bool{}
	for _, id := range item.RemoveChannelIDs {
		if id <= 0 || owners[id] != item.ID {
			writeDashboardError(w, 400, "invalid_channel_selection")
			return
		}
		removed[id] = true
	}
	retained := item.Channels[:0]
	for _, c := range item.Channels {
		if !removed[c.ChannelID] {
			retained = append(retained, c)
		}
	}
	item.Channels = retained
	transferred := map[int64]int64{}
	for _, v := range item.ChannelTransfers {
		if v.ChannelID <= 0 || v.FromUpstreamID <= 0 || v.FromUpstreamID == item.ID || owners[v.ChannelID] != v.FromUpstreamID || transferred[v.ChannelID] != 0 || removed[v.ChannelID] {
			upstreamWriteError(w, billing.ErrUpstreamTransferConflict)
			return
		}
		transferred[v.ChannelID] = v.FromUpstreamID
	}
	adds := append([]int64(nil), item.AddChannelIDs...)
	for id := range transferred {
		adds = append(adds, id)
	}
	present := map[int64]bool{}
	for _, c := range item.Channels {
		present[c.ChannelID] = true
	}
	for _, id := range adds {
		if id <= 0 || removed[id] {
			writeDashboardError(w, 400, "invalid_channel_selection")
			return
		}
		c, ok := known[id]
		if !ok {
			if sourceError != "" {
				writeDashboardError(w, 409, "readonly_source_unavailable")
			} else {
				writeDashboardError(w, 400, "invalid_channel_selection")
			}
			return
		}
		if owner := owners[id]; owner != 0 && owner != item.ID && transferred[id] == 0 {
			upstreamWriteError(w, billing.ErrUpstreamChannelConflict)
			return
		}
		if !present[id] {
			item.Channels = append(item.Channels, billing.UpstreamChannel{ChannelID: id, ChannelName: c.ChannelName})
			present[id] = true
		}
	}
	item.UpdatedBy = ctauth.Actor(r)
	if item.UpdatedBy == "" {
		item.UpdatedBy = "legacy-admin"
	}
	saved, err := h.Store.PutBillingUpstream(r.Context(), item)
	if err != nil {
		upstreamWriteError(w, err)
		return
	}
	warning := ""
	if audit, ok := h.Store.(billingAuditStore); ok {
		var old any = map[string]any{}
		operation := "billing.upstream.create"
		if before != nil {
			old = *before
			operation = "billing.upstream.update"
		}
		auditSaved := saved
		auditSaved.ChannelTransfers = item.ChannelTransfers
		auditSaved.PrefixTransfers = item.PrefixTransfers
		if err = auditBillingMutation(audit, r, saved.InstanceID, operation, strconv.FormatInt(saved.ID, 10), old, auditSaved); err != nil {
			warning = "billing_upstream_audit_failed"
		}
	}
	// Persist the user's rules first. Discovery must not reserve a requested alias before this save.
	if sourceError != "" {
		warning = sourceError
	} else if syncer, ok := h.Store.(interface {
		SyncBillingUpstreamChannels(context.Context, string, []billing.ConfiguredChannel) error
	}); ok {
		if err = syncer.SyncBillingUpstreamChannels(r.Context(), item.InstanceID, source); err != nil {
			warning = "upstream_sync_failed"
		} else if current, e := listUpstreamConfig(r.Context(), h.Store, item.InstanceID); e == nil {
			for _, v := range current {
				if v.ID == saved.ID {
					saved = v
					break
				}
			}
		} else {
			warning = "upstream_sync_failed"
		}
	}
	saved.SyncError = warning
	writeDashboardJSON(w, 200, saved)
}
