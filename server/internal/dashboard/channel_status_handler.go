package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
)

type ChannelStatusUpdater interface {
	UpdateChannelStatus(context.Context, string, int64, int, string, time.Time) (storage.ChannelCommand, error)
}

type ChannelStatusHandler struct {
	Updater   ChannelStatusUpdater
	Directory TuningChannelDirectory
}

func (h ChannelStatusHandler) Update(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	site := strings.TrimSpace(r.URL.Query().Get("site_id"))
	if site == "" {
		writeDashboardError(w, 400, "site_id_required")
		return
	}
	channel, err := strconv.ParseInt(r.PathValue("channelID"), 10, 64)
	if err != nil || channel <= 0 {
		writeDashboardError(w, 400, "invalid_channel_id")
		return
	}
	var request struct {
		Status  int  `json:"status"`
		Confirm bool `json:"confirm"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		writeDashboardError(w, 400, "invalid_json")
		return
	}
	if !request.Confirm {
		writeDashboardError(w, 400, "confirm_required")
		return
	}
	if request.Status != 1 && request.Status != 2 {
		writeDashboardError(w, 400, "invalid_channel_status")
		return
	}
	if h.Updater == nil || h.Directory == nil {
		writeDashboardError(w, 501, "channel_status_update_not_supported")
		return
	}
	channels, err := h.Directory.LatestChannels(site)
	if err != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	found := false
	for _, v := range channels {
		if v.ID == channel {
			found = true
			break
		}
	}
	if !found {
		writeDashboardError(w, 404, "channel_not_found")
		return
	}
	command, err := h.Updater.UpdateChannelStatus(r.Context(), site, channel, request.Status, ctauth.Actor(r), time.Now().UTC())
	if err != nil {
		switch {
		case errors.Is(err, tuning.ErrChannelNotFound):
			writeDashboardError(w, 404, "channel_not_found")
		case errors.Is(err, tuning.ErrChannelControlBusy):
			writeDashboardError(w, 409, "channel_control_busy")
		case errors.Is(err, tuning.ErrManualStatusUnavailable):
			writeDashboardError(w, 501, "channel_status_requires_direct_control")
		default:
			if command.ID != "" {
				writeDashboardError(w, 502, "channel_status_unconfirmed_manual_hold")
			} else {
				writeDashboardError(w, 502, "channel_status_update_failed")
			}
		}
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"command_id": command.ID, "channel_id": channel, "channel_status": request.Status, "status": command.Status})
}
