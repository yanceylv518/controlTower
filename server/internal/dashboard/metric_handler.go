package dashboard

import (
	"fmt"
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
	Latest1mMetrics(dimensionType string) ([]aggregator.Metric, error)
	Latest5mMetrics(dimensionType string) ([]aggregator.Metric, error)
	QueryMetricHistory(window, dimensionType, dimensionKey string, since time.Time) ([]aggregator.Metric, error)
	UsageSummary(since time.Time) ([]storage.UsageRow, error)
}

type metricPrefixSource interface {
	QueryMetricHistoryPrefix(window, dimensionType, dimensionKeyPrefix string, since time.Time) ([]aggregator.Metric, error)
}

type instanceLatestMetricSource interface {
	Latest1mMetricsForInstance(dimensionType, instanceID string) ([]aggregator.Metric, error)
	Latest5mMetricsForInstance(dimensionType, instanceID string) ([]aggregator.Metric, error)
}

type MetricListResponse struct {
	Items []MetricItem `json:"items"`
}

type MetricItem struct {
	InstanceID        string    `json:"instance_id"`
	InstanceName      string    `json:"instance_name"`
	BucketTime        time.Time `json:"bucket_time"`
	DimensionType     string    `json:"dimension_type"`
	DimensionKey      string    `json:"dimension_key"`
	DisplayKey        string    `json:"display_key"`
	DisplayName       string    `json:"display_name"`
	RequestCount      int64     `json:"request_count"`
	SuccessCount      int64     `json:"success_count"`
	ErrorCount        int64     `json:"error_count"`
	SuccessRate       *float64  `json:"success_rate"`
	ErrorRate         *float64  `json:"error_rate"`
	TPM               int64     `json:"tpm"`
	PromptTokens      int64     `json:"prompt_tokens"`
	CompletionTokens  int64     `json:"completion_tokens"`
	Quota             int64     `json:"quota"`
	AvgUseTime        *float64  `json:"avg_use_time"`
	P95UseTime        *float64  `json:"p95_use_time"`
	P50UseTime        *float64  `json:"p50_use_time,omitempty"`
	P99UseTime        *float64  `json:"p99_use_time,omitempty"`
	StreamRate        *float64  `json:"stream_rate"`
	CacheTokenRate    *float64  `json:"cache_token_rate"`
	BigInputCount     *int64    `json:"big_input_count"`
	BigInputCacheHits *int64    `json:"big_input_cache_hits"`
	CacheHitRate      *float64  `json:"cache_hit_rate"`
	TTFTCount         *int64    `json:"ttft_count"`
	TTFTAvgMS         *float64  `json:"ttft_avg_ms"`
	TTFTP50MS         *float64  `json:"ttft_p50_ms"`
	TTFTP90MS         *float64  `json:"ttft_p90_ms"`
	TTFTP95MS         *float64  `json:"ttft_p95_ms"`
	OTPS              *float64  `json:"otps"`
	OTPSSampleTokens  int64     `json:"otps_sample_tokens"`
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
	dimensionType := query.Get("dimension_type")
	instanceID := query.Get("instance_id")
	instanceSource, supportsInstanceQuery := h.metricSource.(instanceLatestMetricSource)
	if window == "5m" && latest && instanceID != "" && supportsInstanceQuery {
		metrics, err = instanceSource.Latest5mMetricsForInstance(dimensionType, instanceID)
	} else if window == "5m" && latest {
		metrics, err = h.metricSource.Latest5mMetrics(dimensionType)
	} else if window == "5m" {
		metrics, err = h.metricSource.Recent5mMetrics()
	} else if latest && instanceID != "" && supportsInstanceQuery {
		metrics, err = instanceSource.Latest1mMetricsForInstance(dimensionType, instanceID)
	} else if latest {
		metrics, err = h.metricSource.Latest1mMetrics(dimensionType)
	} else {
		metrics, err = h.metricSource.Recent1mMetrics()
	}
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	items := h.filterMetricItems(metrics, query.Get("dimension_type"), query.Get("dimension_key"), query.Get("instance_id"))
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
	dimensionKeyPrefix := query.Get("dimension_key_prefix")
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
	if dimensionType == "" || (dimensionKey == "") == (dimensionKeyPrefix == "") || (window != "1m" && window != "5m") || hours < 1 || hours > 24 {
		writeDashboardError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
	var metrics []aggregator.Metric
	var err error
	if dimensionKeyPrefix != "" {
		source, ok := h.metricSource.(metricPrefixSource)
		if !ok {
			writeDashboardError(w, http.StatusInternalServerError, "metric_prefix_source_not_configured")
			return
		}
		metrics, err = source.QueryMetricHistoryPrefix(window, dimensionType, dimensionKeyPrefix, since)
	} else {
		metrics, err = h.metricSource.QueryMetricHistory(window, dimensionType, dimensionKey, since)
	}
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	if query.Get("aggregate") == "true" {
		if len(metrics) == 0 {
			writeDashboardJSON(w, http.StatusOK, MetricListResponse{Items: []MetricItem{}})
			return
		}
		if dimensionKeyPrefix != "" {
			grouped := make(map[string]aggregator.Metric)
			for _, metric := range metrics {
				if current, ok := grouped[metric.DimensionKey]; ok {
					grouped[metric.DimensionKey] = aggregator.MergeMetric(current, metric)
				} else {
					grouped[metric.DimensionKey] = metric
				}
			}
			merged := make([]aggregator.Metric, 0, len(grouped))
			for _, metric := range grouped {
				metric.BucketTime = time.Now().UTC()
				merged = append(merged, metric)
			}
			sort.Slice(merged, func(i, j int) bool { return merged[i].DimensionKey < merged[j].DimensionKey })
			writeDashboardJSON(w, http.StatusOK, MetricListResponse{Items: h.filterMetricItems(merged, dimensionType, "", query.Get("instance_id"))})
			return
		}
		merged := metrics[0]
		for _, metric := range metrics[1:] {
			merged = aggregator.MergeMetric(merged, metric)
		}
		merged.BucketTime = time.Now().UTC()
		writeDashboardJSON(w, http.StatusOK, MetricListResponse{Items: h.filterMetricItems([]aggregator.Metric{merged}, dimensionType, dimensionKey, query.Get("instance_id"))})
		return
	}
	items := h.filterMetricItems(metrics, "", "", query.Get("instance_id"))
	sort.Slice(items, func(i, j int) bool { return items[i].BucketTime.Before(items[j].BucketTime) })
	writeDashboardJSON(w, http.StatusOK, MetricListResponse{Items: items})
}

func filterMetricItems(metrics []aggregator.Metric, dimensionType string, dimensionKey string, instanceID ...string) []MetricItem {
	return Handler{}.filterMetricItems(metrics, dimensionType, dimensionKey, instanceID...)
}

func (h Handler) filterMetricItems(metrics []aggregator.Metric, dimensionType string, dimensionKey string, instanceID ...string) []MetricItem {
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
		p50 := metric.P50UseTime
		if p50 == nil {
			p50 = latencyhist.Quantile(metric.LatencyBuckets, 0.5)
		}
		p99 := metric.P99UseTime
		if p99 == nil {
			p99 = latencyhist.Quantile(metric.LatencyBuckets, 0.99)
		}
		items = append(items, MetricItem{
			InstanceID:        metric.InstanceID,
			InstanceName:      h.instanceName(metric.InstanceID),
			BucketTime:        metric.BucketTime,
			DimensionType:     metric.DimensionType,
			DimensionKey:      metric.DimensionKey,
			DisplayKey:        h.displayDimensionKey(metric.DimensionType, metric.DimensionKey),
			DisplayName:       h.displayDimensionName(metric.DimensionType, metric.DimensionKey),
			RequestCount:      metric.RequestCount,
			SuccessCount:      metric.SuccessCount,
			ErrorCount:        metric.ErrorCount,
			SuccessRate:       metric.SuccessRate,
			ErrorRate:         metric.ErrorRate,
			TPM:               metric.TPM,
			PromptTokens:      metric.PromptTokens,
			CompletionTokens:  metric.CompletionTokens,
			Quota:             metric.Quota,
			AvgUseTime:        metric.AvgUseTime,
			P95UseTime:        metric.P95UseTime,
			P50UseTime:        p50,
			P99UseTime:        p99,
			StreamRate:        metric.StreamRate,
			CacheTokenRate:    metric.CacheTokenRate,
			BigInputCount:     metric.BigInputCount,
			BigInputCacheHits: metric.BigInputCacheHits,
			CacheHitRate:      nullableRatio(metric.BigInputCacheHits, metric.BigInputCount),
			TTFTCount:         metric.TTFTCount,
			TTFTAvgMS:         nullableAverage(metric.TTFTSumMS, metric.TTFTCount),
			TTFTP50MS:         metric.TTFTP50MS,
			TTFTP90MS:         metric.TTFTP90MS,
			TTFTP95MS:         metric.TTFTP95MS,
			OTPS:              otps(metric.OTPSOutputTokens, metric.OTPSDurationSecs),
			OTPSSampleTokens:  metric.OTPSOutputTokens,
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

func otps(tokens int64, seconds float64) *float64 {
	if tokens <= 0 || seconds <= 0 {
		return nil
	}
	value := float64(tokens) / seconds
	return &value
}

func nullableRatio(numerator, denominator *int64) *float64 {
	if numerator == nil || denominator == nil || *denominator == 0 {
		return nil
	}
	value := float64(*numerator) / float64(*denominator)
	return &value
}
func nullableAverage(sum, count *int64) *float64 {
	if sum == nil || count == nil || *count == 0 {
		return nil
	}
	value := float64(*sum) / float64(*count)
	return &value
}

func (h Handler) instanceName(instanceID string) string {
	if h.names == nil {
		return instanceID
	}
	return h.names.InstanceName(instanceID)
}

func (h Handler) displayDimensionKey(dimensionType string, dimensionKey string) string {
	parts := strings.Split(dimensionKey, ":")
	if len(parts) < 3 {
		return dimensionKey
	}
	instanceID := strings.Join(parts[:len(parts)-2], ":")
	id, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	switch dimensionType {
	case "instance_channel":
		name := "渠道 " + parts[len(parts)-1]
		if h.names != nil {
			name = h.names.ChannelName(instanceID, id)
		}
		if name == "渠道 "+parts[len(parts)-1] {
			return name
		}
		return fmt.Sprintf("%s (ID %d)", name, id)
	case "instance_user":
		name := "用户 " + parts[len(parts)-1]
		if h.names != nil {
			name = h.names.UserName(instanceID, id)
		}
		if name == "用户 "+parts[len(parts)-1] {
			return name
		}
		return fmt.Sprintf("%s (ID %d)", name, id)
	case "instance_model":
		return strings.Join(parts[2:], ":")
	}
	return dimensionKey
}

func displayDimensionKey(dimensionType string, dimensionKey string) string {
	return Handler{}.displayDimensionKey(dimensionType, dimensionKey)
}

// displayDimensionName is presentation-only. DisplayKey remains backward
// compatible because alert links and older clients still consume it.
func (h Handler) displayDimensionName(dimensionType string, dimensionKey string) string {
	parts := strings.Split(dimensionKey, ":")
	if len(parts) < 3 {
		return dimensionKey
	}
	instanceID := strings.Join(parts[:len(parts)-2], ":")
	idText := parts[len(parts)-1]
	id, _ := strconv.ParseInt(idText, 10, 64)
	switch dimensionType {
	case "instance_channel":
		if h.names != nil {
			return h.names.ChannelName(instanceID, id)
		}
		return "渠道 " + idText
	case "instance_user":
		if h.names != nil {
			return h.names.UserName(instanceID, id)
		}
		return "用户 " + idText
	case "instance_model":
		return strings.Join(parts[2:], ":")
	default:
		return dimensionKey
	}
}
