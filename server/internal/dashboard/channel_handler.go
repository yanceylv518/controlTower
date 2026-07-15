package dashboard

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"controltower/server/internal/storage"
)

type ChannelSnapshotStore interface {
	QueryChannelSnapshots(query storage.ChannelSnapshotQuery) ([]storage.ChannelSnapshot, error)
}

type ChannelSnapshotListResponse struct {
	Items []ChannelSnapshotSummary `json:"items"`
}

type ChannelSnapshotSummary struct {
	ID           string    `json:"id"`
	InstanceID   string    `json:"instance_id"`
	InstanceName string    `json:"instance_name"`
	ChannelID    int64     `json:"channel_id"`
	ChannelName  string    `json:"channel_name"`
	Status       string    `json:"status"`
	Weight       int64     `json:"weight"`
	ModelsText   string    `json:"models_text"`
	GroupName    *string   `json:"group_name"`
	Priority     *int64    `json:"priority"`
	CapturedAt   time.Time `json:"captured_at"`
}

func (h Handler) WithChannelSnapshotStore(store ChannelSnapshotStore) Handler {
	h.channelSnapshotStore = store
	return h
}

func (h Handler) HandleChannelSnapshots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if h.channelSnapshotStore == nil {
		writeDashboardError(w, http.StatusInternalServerError, "channel_snapshot_store_not_configured")
		return
	}
	query := parseChannelSnapshotQuery(r)
	items, err := h.channelSnapshotStore.QueryChannelSnapshots(query)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	if query.LatestOnly {
		items = latestChannelSnapshots(items)
	}
	summaries := summarizeChannelSnapshots(items)
	for i := range summaries {
		summaries[i].InstanceName = h.instanceName(summaries[i].InstanceID)
	}
	writeDashboardJSON(w, http.StatusOK, ChannelSnapshotListResponse{Items: summaries})
}

func parseChannelSnapshotQuery(r *http.Request) storage.ChannelSnapshotQuery {
	query := r.URL.Query()
	latestOnly := true
	if value := query.Get("latest_only"); value != "" {
		if parsed, ok := parseBool(value); ok {
			latestOnly = parsed
		}
	}
	return storage.ChannelSnapshotQuery{
		InstanceID: query.Get("instance_id"),
		ChannelID:  parseDashboardInt64(query.Get("channel_id")),
		LatestOnly: latestOnly,
		StartTime:  parseTime(query.Get("start_time")),
		EndTime:    parseTime(query.Get("end_time")),
		Limit:      parseInt(query.Get("limit")),
		Offset:     parseInt(query.Get("offset")),
	}
}

func latestChannelSnapshots(items []storage.ChannelSnapshot) []storage.ChannelSnapshot {
	latest := make(map[string]storage.ChannelSnapshot, len(items))
	for _, item := range items {
		key := item.InstanceID + ":" + strconv.FormatInt(item.ChannelID, 10)
		current, ok := latest[key]
		if !ok || item.CapturedAt.After(current.CapturedAt) {
			latest[key] = item
		}
	}
	result := make([]storage.ChannelSnapshot, 0, len(latest))
	for _, item := range latest {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].InstanceID == result[j].InstanceID {
			return result[i].ChannelID < result[j].ChannelID
		}
		return result[i].InstanceID < result[j].InstanceID
	})
	return result
}

func summarizeChannelSnapshots(items []storage.ChannelSnapshot) []ChannelSnapshotSummary {
	summaries := make([]ChannelSnapshotSummary, 0, len(items))
	for _, item := range items {
		summaries = append(summaries, ChannelSnapshotSummary{ID: item.ID, InstanceID: item.InstanceID, ChannelID: item.ChannelID, ChannelName: item.ChannelName, Status: item.Status, Weight: item.Weight, ModelsText: item.ModelsText, GroupName: item.GroupName, Priority: item.Priority, CapturedAt: item.CapturedAt})
	}
	return summaries
}

func parseDashboardInt64(value string) int64 {
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}
