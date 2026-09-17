package dashboard

import (
	"controltower/internal/channelcontrol"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/storage"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type ChannelGroupPresetStore = storage.ChannelGroupPresetStore
type ChannelGroupPresetsHandler struct {
	Store storage.ChannelGroupPresetStore
}

func (h ChannelGroupPresetsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	site := strings.TrimSpace(r.URL.Query().Get("site_id"))
	if site == "" || utf8.RuneCountInString(site) > 191 || strings.IndexFunc(site, unicode.IsControl) >= 0 {
		writeDashboardError(w, 400, "invalid_site_id")
		return
	}
	switch r.Method {
	case http.MethodGet:
		value, err := h.Store.LoadChannelGroupPresets(r.Context(), site)
		if err != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		writeDashboardJSON(w, 200, value)
	case http.MethodPut:
		var value storage.ChannelGroupPresets
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024))
		if decoder.Decode(&value) != nil || value.Revision < 0 || value.Items == nil || len(value.Items) > 100 {
			writeDashboardError(w, 400, "invalid_presets")
			return
		}
		names, ids := map[string]bool{}, map[string]bool{}
		for i := range value.Items {
			item := &value.Items[i]
			item.Name = strings.TrimSpace(item.Name)
			name := strings.ToLower(item.Name)
			if item.ID == "" || len(item.ID) > 64 || strings.IndexFunc(item.ID, unicode.IsControl) >= 0 || item.Name == "" || utf8.RuneCountInString(item.Name) > 64 || strings.IndexFunc(item.Name, unicode.IsControl) >= 0 || names[name] || ids[item.ID] || len(item.Groups) == 0 {
				writeDashboardError(w, 400, "invalid_presets")
				return
			}
			for _, group := range item.Groups {
				if strings.Contains(group, ",") {
					writeDashboardError(w, 400, "invalid_group")
					return
				}
			}
			group, err := channelcontrol.NormalizeGroup(strings.Join(item.Groups, ","))
			if err != nil || group == "" {
				writeDashboardError(w, 400, "invalid_group")
				return
			}
			item.Groups = strings.Split(group, ",")
			names[name], ids[item.ID] = true, true
		}
		err := h.Store.SaveChannelGroupPresets(r.Context(), site, value, ctauth.Actor(r), time.Now().UTC())
		if errors.Is(err, storage.ErrGroupPresetConflict) {
			writeDashboardError(w, 409, "presets_changed")
			return
		}
		if err != nil {
			writeDashboardError(w, 500, "update_failed")
			return
		}
		value.Revision++
		writeDashboardJSON(w, 200, value)
	default:
		writeDashboardError(w, 405, "method_not_allowed")
	}
}
