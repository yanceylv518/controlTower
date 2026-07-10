package dashboard

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"controltower/server/internal/aggregator"
)

type MetricSource interface {
	Recent1mMetrics() ([]aggregator.Metric, error)
	Recent5mMetrics() ([]aggregator.Metric, error)
}

type MetricListResponse struct {
	Items []MetricItem `json:"items"`
}

type MetricItem struct {
	InstanceID       string    `json:"instance_id"`
	BucketTime       time.Time `json:"bucket_time"`
	DimensionType    string    `json:"dimension_type"`
	DimensionKey     string    `json:"dimension_key"`
	DisplayKey       string    `json:"display_key"`
	RequestCount     int64     `json:"request_count"`
	SuccessCount     int64     `json:"success_count"`
	ErrorCount       int64     `json:"error_count"`
	SuccessRate      *float64  `json:"success_rate"`
	ErrorRate        *float64  `json:"error_rate"`
	TPM              int64     `json:"tpm"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	Quota            int64     `json:"quota"`
	AvgUseTime       *float64  `json:"avg_use_time"`
	P95UseTime       *float64  `json:"p95_use_time"`
	StreamRate       *float64  `json:"stream_rate"`
	CacheTokenRate   *float64  `json:"cache_token_rate"`
}

func (h Handler) WithMetricSource(source MetricSource) Handler {
	h.metricSource = source
	return h
}

func (h Handler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if h.metricSource == nil {
		writeDashboardError(w, http.StatusInternalServerError, "metric_source_not_configured")
		return
	}
	query := r.URL.Query()
	window := query.Get("window")
	var metrics []aggregator.Metric
	var err error
	if window == "5m" {
		metrics, err = h.metricSource.Recent5mMetrics()
	} else {
		metrics, err = h.metricSource.Recent1mMetrics()
	}
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	items := filterMetricItems(metrics, query.Get("dimension_type"), query.Get("dimension_key"))
	writeDashboardJSON(w, http.StatusOK, MetricListResponse{Items: items})
}

func filterMetricItems(metrics []aggregator.Metric, dimensionType string, dimensionKey string) []MetricItem {
	items := make([]MetricItem, 0, len(metrics))
	for _, metric := range metrics {
		if dimensionType != "" && metric.DimensionType != dimensionType {
			continue
		}
		if dimensionKey != "" && metric.DimensionKey != dimensionKey {
			continue
		}
		items = append(items, MetricItem{
			InstanceID:       metric.InstanceID,
			BucketTime:       metric.BucketTime,
			DimensionType:    metric.DimensionType,
			DimensionKey:     metric.DimensionKey,
			DisplayKey:       displayDimensionKey(metric.DimensionType, metric.DimensionKey),
			RequestCount:     metric.RequestCount,
			SuccessCount:     metric.SuccessCount,
			ErrorCount:       metric.ErrorCount,
			SuccessRate:      metric.SuccessRate,
			ErrorRate:        metric.ErrorRate,
			TPM:              metric.TPM,
			PromptTokens:     metric.PromptTokens,
			CompletionTokens: metric.CompletionTokens,
			Quota:            metric.Quota,
			AvgUseTime:       metric.AvgUseTime,
			P95UseTime:       metric.P95UseTime,
			StreamRate:       metric.StreamRate,
			CacheTokenRate:   metric.CacheTokenRate,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].BucketTime.Equal(items[j].BucketTime) {
			return items[i].RequestCount > items[j].RequestCount
		}
		return items[i].BucketTime.After(items[j].BucketTime)
	})
	return items
}

func displayDimensionKey(dimensionType string, dimensionKey string) string {
	parts := strings.Split(dimensionKey, ":")
	switch dimensionType {
	case "instance_model":
		if len(parts) >= 3 {
			return strings.Join(parts[2:], ":")
		}
	case "instance_channel":
		if len(parts) >= 3 {
			return "\u6e20\u9053 " + parts[2]
		}
	case "instance_user":
		if len(parts) >= 3 {
			return "\u7528\u6237 " + parts[2]
		}
	}
	return dimensionKey
}
