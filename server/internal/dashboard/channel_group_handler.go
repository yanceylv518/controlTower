package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"controltower/internal/channelcontrol"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
)

type ChannelGroupUpdater interface {
	UpdateChannelGroup(context.Context, string, int64, string, string, time.Time) (storage.ChannelCommand, error)
}

type ChannelGroupUpdateResponse struct {
	CommandID  string    `json:"command_id"`
	InstanceID string    `json:"instance_id"`
	ChannelID  int64     `json:"channel_id"`
	Group      string    `json:"group"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

type ChannelGroupHandler struct {
	Updater   ChannelGroupUpdater
	Directory TuningChannelDirectory
}

// validateChannelGroupAgainstDirectory 以站点最新渠道快照中的分组作为允许集合，
// 防止页面或直接调用者把 New API 未使用过的用户组名称写入渠道。
func validateChannelGroupAgainstDirectory(group string, channels []tuning.Channel) error {
	knownValues := make([]string, 0, len(channels))
	for _, channel := range channels {
		knownValues = append(knownValues, channel.GroupName)
	}
	return channelcontrol.ValidateKnownGroups(group, knownValues)
}

// Update 在委托直连或 Agent 更新器前校验站点与渠道边界。确认字段用于防止
// 绕过页面确认框的客户端误发线上写操作。
func (h ChannelGroupHandler) Update(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	siteID := strings.TrimSpace(r.URL.Query().Get("site_id"))
	if siteID == "" {
		writeDashboardError(w, http.StatusBadRequest, "site_id_required")
		return
	}
	channelID, err := strconv.ParseInt(r.PathValue("channelID"), 10, 64)
	if err != nil || channelID <= 0 {
		writeDashboardError(w, http.StatusBadRequest, "invalid_channel_id")
		return
	}
	var request struct {
		Confirm bool    `json:"confirm"`
		Group   *string `json:"group"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		writeDashboardError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if !request.Confirm {
		writeDashboardError(w, http.StatusBadRequest, "confirm_required")
		return
	}
	if request.Group == nil {
		writeDashboardError(w, http.StatusBadRequest, "group_required")
		return
	}
	group, err := channelcontrol.NormalizeGroup(*request.Group)
	if err != nil {
		writeDashboardError(w, http.StatusBadRequest, "invalid_group")
		return
	}
	if h.Updater == nil || h.Directory == nil {
		writeDashboardError(w, http.StatusNotImplemented, "channel_group_update_not_supported")
		return
	}
	channels, err := h.Directory.LatestChannels(siteID)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	found := false
	for _, channel := range channels {
		if channel.ID == channelID {
			found = true
			break
		}
	}
	if !found {
		writeDashboardError(w, http.StatusNotFound, "channel_not_found")
		return
	}
	if err := validateChannelGroupAgainstDirectory(group, channels); err != nil {
		if errors.Is(err, channelcontrol.ErrGroupNotFound) {
			writeDashboardError(w, http.StatusBadRequest, "group_not_found")
		} else {
			writeDashboardError(w, http.StatusBadRequest, "invalid_group")
		}
		return
	}
	command, err := h.Updater.UpdateChannelGroup(r.Context(), siteID, channelID, group, ctauth.Actor(r), time.Now().UTC())
	if err != nil {
		if errors.Is(err, tuning.ErrChannelNotFound) {
			writeDashboardError(w, http.StatusNotFound, "channel_not_found")
			return
		}
		writeDashboardError(w, http.StatusInternalServerError, "group_update_failed")
		return
	}
	status := http.StatusAccepted
	if command.Status == "succeeded" {
		status = http.StatusOK
	}
	createdAt := command.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	writeDashboardJSON(w, status, ChannelGroupUpdateResponse{CommandID: command.ID, InstanceID: command.InstanceID, ChannelID: command.ChannelID, Group: group, Status: command.Status, CreatedAt: createdAt})
}
