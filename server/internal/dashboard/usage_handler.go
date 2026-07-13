package dashboard

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"controltower/server/internal/storage"
)

type UsageItem struct {
	DimensionType    string `json:"dimension_type"`
	DimensionKey     string `json:"dimension_key"`
	DisplayKey       string `json:"display_key"`
	RequestCount     int64  `json:"request_count"`
	TotalTokens      int64  `json:"total_tokens"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	Quota            int64  `json:"quota"`
}

func (h Handler) HandleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if h.metricSource == nil {
		writeDashboardError(w, http.StatusInternalServerError, "metric_source_not_configured")
		return
	}
	hours := 24
	if raw := r.URL.Query().Get("hours"); raw != "" {
		var err error
		if hours, err = strconv.Atoi(raw); err != nil {
			hours = 0
		}
	}
	if hours < 1 || hours > 720 {
		writeDashboardError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	rows, err := h.metricSource.UsageSummary(time.Now().UTC().Add(-time.Duration(hours) * time.Hour))
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	items := make([]UsageItem, 0, len(rows))
	instanceID := r.URL.Query().Get("instance_id")
	for _, row := range rows {
		if instanceID != "" && !strings.HasPrefix(row.DimensionKey, instanceID+":") {
			continue
		}
		items = append(items, usageItem(row))
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"items": items})
}

func usageItem(row storage.UsageRow) UsageItem {
	return UsageItem{DimensionType: row.DimensionType, DimensionKey: row.DimensionKey, DisplayKey: displayDimensionKey(row.DimensionType, row.DimensionKey), RequestCount: row.RequestCount, TotalTokens: row.PromptTokens + row.CompletionTokens, PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens, Quota: row.Quota}
}
