package dashboard

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"controltower/internal/latencyhist"
	"controltower/server/internal/aggregator"
	"controltower/server/internal/storage"
)

type MetricSource interface {
	Recent1mMetrics() ([]aggregator.Metric, error)
	Recent5mMetrics() ([]aggregator.Metric, error)
	Latest1mMetrics() ([]aggregator.Metric, error)
	Latest5mMetrics() ([]aggregator.Metric, error)
	QueryMetricHistory(window, dimensionType, dimensionKey string, since time.Time) ([]aggregator.Metric, error)
	UsageSummary(since time.Time) ([]storage.UsageRow, error)
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
	P50UseTime       *float64  `json:"p50_use_time,omitempty"`
	P99UseTime       *float64  `json:"p99_use_time,omitempty"`
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
	latest := query.Get("latest") == "true"
	if window == "5m" && latest {
		metrics, err = h.metricSource.Latest5mMetrics()
	} else if window == "5m" {
		metrics, err = h.metricSource.Recent5mMetrics()
	} else if latest {
		metrics, err = h.metricSource.Latest1mMetrics()
	} else {
		metrics, err = h.metricSource.Recent1mMetrics()
	}
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	items := filterMetricItems(metrics, query.Get("dimension_type"), query.Get("dimension_key"), query.Get("instance_id"))
	writeDashboardJSON(w, http.StatusOK, MetricListResponse{Items: items})
}

func (h Handler) HandleMetricHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if h.metricSource == nil {
		writeDashboardError(w, http.StatusInternalServerError, "metric_source_not_configured")
		return
	}
	query := r.URL.Query()
	dimensionType, dimensionKey := query.Get("dimension_type"), query.Get("dimension_key")
	window := query.Get("window")
	if window == "" {
		window = "1m"
	}
	hours := 1
	if raw := query.Get("hours"); raw != "" {
		var err error
		if hours, err = strconv.Atoi(raw); err != nil {
			hours = 0
		}
	}
	if dimensionType == "" || dimensionKey == "" || (window != "1m" && window != "5m") || hours < 1 || hours > 24 {
		writeDashboardError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	metrics, err := h.metricSource.QueryMetricHistory(window, dimensionType, dimensionKey, time.Now().UTC().Add(-time.Duration(hours)*time.Hour))
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	items := filterMetricItems(metrics, "", "", query.Get("instance_id"))
	sort.Slice(items, func(i, j int) bool { return items[i].BucketTime.Before(items[j].BucketTime) })
	writeDashboardJSON(w, http.StatusOK, MetricListResponse{Items: items})
}

func filterMetricItems(metrics []aggregator.Metric, dimensionType string, dimensionKey string, instanceID ...string) []MetricItem {
	items := make([]MetricItem, 0, len(metrics))
	for _, metric := range metrics {
		if len(instanceID) > 0 && instanceID[0] != "" && metric.InstanceID != instanceID[0] {
			continue
		}
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
			P50UseTime:       latencyhist.Quantile(metric.LatencyBuckets, 0.5),
			P99UseTime:       latencyhist.Quantile(metric.LatencyBuckets, 0.99),
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
