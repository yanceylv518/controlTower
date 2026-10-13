package dashboard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"controltower/server/internal/auditmeta"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/channelschedule"
	"controltower/server/internal/storage"
)

type ChannelSchedulesStore interface {
	ListChannelSchedules(context.Context, string) ([]channelschedule.Schedule, error)
	SaveChannelSchedule(context.Context, string, int64, channelschedule.Config, storage.OperationAudit, time.Time) (int64, error)
	DeleteChannelSchedule(context.Context, string, int64, int64, *time.Time, storage.OperationAudit, time.Time) error
	ChannelScheduleExecutions(context.Context, string, int64) ([]channelschedule.Execution, error)
	HasDirectChannelControl(string) (bool, error)
}

type ChannelSchedulesHandler struct {
	Store         ChannelSchedulesStore
	WorkerEnabled bool
}

func (h ChannelSchedulesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	site := strings.TrimSpace(r.URL.Query().Get("site_id"))
	if site == "" || utf8.RuneCountInString(site) > 191 || strings.IndexFunc(site, unicode.IsControl) >= 0 {
		writeDashboardError(w, 400, "invalid_site_id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	channelText := r.PathValue("channelID")
	if channelText == "" && r.Method == http.MethodGet {
		items, err := h.Store.ListChannelSchedules(ctx, site)
		if err != nil {
			writeDashboardError(w, 500, "schedule_query_failed")
			return
		}
		direct, err := h.Store.HasDirectChannelControl(site)
		if err != nil {
			writeDashboardError(w, 500, "schedule_config_query_failed")
			return
		}
		writeDashboardJSON(w, 200, map[string]any{"items": items, "direct_configured": direct, "worker_enabled": h.WorkerEnabled})
		return
	}
	channel, err := strconv.ParseInt(channelText, 10, 64)
	if err != nil || channel <= 0 {
		writeDashboardError(w, 400, "invalid_channel_id")
		return
	}
	if r.Method == http.MethodGet {
		items, err := h.Store.ChannelScheduleExecutions(ctx, site, channel)
		if err != nil {
			writeDashboardError(w, 500, "schedule_history_query_failed")
			return
		}
		writeDashboardJSON(w, 200, map[string]any{"items": items})
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodDelete {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	var input struct {
		channelschedule.Config
		Confirm bool `json:"confirm"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || !input.Confirm {
		writeDashboardError(w, 400, "invalid_schedule_request")
		return
	}
	if r.Method == http.MethodPut {
		if err = input.Config.Normalize(); err != nil {
			writeDashboardJSON(w, 400, map[string]any{"error": "invalid_schedule", "message": err.Error()})
			return
		}
	} else if input.Revision <= 0 {
		writeDashboardError(w, 400, "invalid_schedule_revision")
		return
	}
	var entropy [16]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		writeDashboardError(w, 500, "audit_id_failed")
		return
	}
	now := time.Now().UTC()
	audit := storage.OperationAudit{ID: "schedule-config-" + hex.EncodeToString(entropy[:]), ActorID: ctauth.Actor(r), SourceComponent: "tuning", CreatedAt: now, UpdatedAt: now}
	auditmeta.Enrich(r, &audit)
	revision := input.Revision
	if r.Method == http.MethodPut {
		revision, err = h.Store.SaveChannelSchedule(ctx, site, channel, input.Config, audit, now)
	} else {
		err = h.Store.DeleteChannelSchedule(ctx, site, channel, input.Revision, input.ExpectedUpdatedAt, audit, now)
	}
	if err != nil {
		switch {
		case errors.Is(err, channelschedule.ErrConflict):
			writeDashboardError(w, 409, "schedule_changed")
		case errors.Is(err, channelschedule.ErrBusy):
			writeDashboardError(w, 409, "channel_control_busy")
		case errors.Is(err, channelschedule.ErrUnsupported):
			writeDashboardError(w, 409, "schedule_requires_direct_control")
		case errors.Is(err, channelschedule.ErrLimit):
			writeDashboardError(w, 409, "schedule_limit_reached")
		case errors.Is(err, channelschedule.ErrChannelNotFound):
			writeDashboardError(w, 404, "schedule_channel_not_found")
		default:
			writeDashboardError(w, 500, "schedule_update_failed")
		}
		return
	}
	auditmeta.MarkSemanticAudit(r)
	writeDashboardJSON(w, 200, map[string]any{"saved": true, "revision": revision, "changed": revision != input.Revision || r.Method == http.MethodDelete})
}

func (h ChannelSchedulesHandler) HandlePreview(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("config")
	if len(raw) > 8192 {
		writeDashboardError(w, 400, "invalid_schedule")
		return
	}
	var config channelschedule.Config
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeDashboardError(w, 400, "invalid_schedule")
		return
	}
	if err := config.Normalize(); err != nil {
		writeDashboardJSON(w, 400, map[string]any{"error": "invalid_schedule", "message": err.Error()})
		return
	}
	writeDashboardJSON(w, 200, config.Preview(time.Now().UTC()))
}
