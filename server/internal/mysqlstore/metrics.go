package mysqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"controltower/internal/latencyhist"
	"controltower/server/internal/aggregator"
)

func (s Store) Recent1mMetrics() ([]aggregator.Metric, error) {
	return s.recentMetrics("metric_1m", 200, false)
}

func (s Store) Latest1mMetrics(dimensionType string) ([]aggregator.Metric, error) {
	return s.latestMetrics("metric_1m", 5000, dimensionType)
}

func (s Store) Recent5mMetrics() ([]aggregator.Metric, error) {
	return s.recentMetrics("metric_5m", 200, false)
}

func (s Store) Latest5mMetrics(dimensionType string) ([]aggregator.Metric, error) {
	return s.latestMetrics("metric_5m", 5000, dimensionType)
}

func (s Store) QueryMetricHistory(window, dimensionType, dimensionKey string, since time.Time) ([]aggregator.Metric, error) {
	table, err := metricTable(window)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(context.Background(), metricHistorySQL(table), dimensionType, dimensionKey, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMetrics(rows)
}

func (s Store) recentMetrics(table string, limit int, latestOnly bool) ([]aggregator.Metric, error) {
	rows, err := s.db.QueryContext(context.Background(), recentMetricsSQL(table, latestOnly), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanMetrics(rows)
}

func (s Store) latestMetrics(table string, limit int, dimensionType string) ([]aggregator.Metric, error) {
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	rows, err := s.db.QueryContext(context.Background(), recentMetricsSQL(table, true), cutoff, dimensionType, dimensionType, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMetrics(rows)
}

func scanMetrics(rows *sql.Rows) ([]aggregator.Metric, error) {
	var metrics []aggregator.Metric
	for rows.Next() {
		var metric aggregator.Metric
		var successRate, errorRate, avgUseTime, p50UseTime, p95UseTime, p99UseTime, streamRate, cacheTokenRate, ttftP95MS sql.NullFloat64
		var bigInputCount, bigInputCacheHits, ttftCount, ttftSumMS sql.NullInt64
		var buckets latencyhist.Buckets
		dest := []any{
			&metric.InstanceID,
			&metric.BucketTime,
			&metric.DimensionType,
			&metric.DimensionKey,
			&metric.RequestCount,
			&metric.SuccessCount,
			&metric.ErrorCount,
			&successRate,
			&errorRate,
			&metric.TPM,
			&metric.PromptTokens,
			&metric.CompletionTokens,
			&metric.Quota,
			&avgUseTime,
			&p50UseTime,
			&p95UseTime,
			&p99UseTime,
			&streamRate,
			&cacheTokenRate,
			&metric.UseTimeSum,
			&metric.StreamCount,
			&metric.CacheTokensTotal,
			&metric.CachePromptTokens,
			&bigInputCount,
			&bigInputCacheHits,
			&ttftCount,
			&ttftSumMS,
			&ttftP95MS,
		}
		for i := range buckets {
			dest = append(dest, &buckets[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		metric.LatencyBuckets = buckets
		metric.SuccessRate = floatPointer(successRate)
		metric.ErrorRate = floatPointer(errorRate)
		metric.AvgUseTime = floatPointer(avgUseTime)
		metric.P50UseTime = exactOrHistogram(p50UseTime, buckets, 0.50)
		metric.P95UseTime = exactOrHistogram(p95UseTime, buckets, 0.95)
		metric.P99UseTime = exactOrHistogram(p99UseTime, buckets, 0.99)
		metric.StreamRate = floatPointer(streamRate)
		metric.CacheTokenRate = floatPointer(cacheTokenRate)
		metric.BigInputCount = int64Pointer(bigInputCount)
		metric.BigInputCacheHits = int64Pointer(bigInputCacheHits)
		metric.TTFTCount = int64Pointer(ttftCount)
		metric.TTFTSumMS = int64Pointer(ttftSumMS)
		metric.TTFTP95MS = floatPointer(ttftP95MS)
		metrics = append(metrics, metric)
	}
	return metrics, rows.Err()
}

func recentMetricsSQL(table string, latestOnly bool) string {
	if latestOnly {
		return `SELECT m.instance_id, m.bucket_time, m.dimension_type, m.dimension_key,
  m.request_count, m.success_count, m.error_count, m.success_rate, m.error_rate,
  m.tpm, m.prompt_tokens, m.completion_tokens, m.quota,
  m.avg_use_time, m.p50_use_time, m.p95_use_time, m.p99_use_time, m.stream_rate, m.cache_token_rate,
  m.use_time_sum, m.stream_count, m.cache_tokens_total, m.cache_prompt_tokens, m.big_input_count, m.big_input_cache_hits, m.ttft_count, m.ttft_sum_ms, m.ttft_p95_ms, ` + prefixedLatencyBucketColumnSQL("m") + `
FROM ` + table + ` m JOIN (
  SELECT instance_id, dimension_type, dimension_key, MAX(bucket_time) AS mb
  FROM ` + table + `
  WHERE bucket_time >= ? AND (? = '' OR dimension_type = ?)
  GROUP BY instance_id, dimension_type, dimension_key
) t ON m.instance_id=t.instance_id AND m.dimension_type=t.dimension_type
 AND m.dimension_key=t.dimension_key AND m.bucket_time=t.mb
ORDER BY m.bucket_time DESC, m.dimension_type ASC, m.dimension_key ASC
LIMIT ?`
	}
	return `SELECT instance_id, bucket_time, dimension_type, dimension_key,
  request_count, success_count, error_count, success_rate, error_rate,
  tpm, prompt_tokens, completion_tokens, quota,
  avg_use_time, p50_use_time, p95_use_time, p99_use_time, stream_rate, cache_token_rate,
  use_time_sum, stream_count, cache_tokens_total, cache_prompt_tokens, big_input_count, big_input_cache_hits, ttft_count, ttft_sum_ms, ttft_p95_ms, ` + latencyBucketColumnSQL() + `
FROM ` + table + `
ORDER BY bucket_time DESC, dimension_type ASC, dimension_key ASC
LIMIT ?`
}

func prefixedLatencyBucketColumnSQL(prefix string) string {
	columns := latencyBucketColumnSQL()
	parts := strings.Split(columns, ", ")
	for i := range parts {
		parts[i] = prefix + "." + parts[i]
	}
	return strings.Join(parts, ", ")
}

func metricHistorySQL(table string) string {
	return `SELECT instance_id, bucket_time, dimension_type, dimension_key,
  request_count, success_count, error_count, success_rate, error_rate,
  tpm, prompt_tokens, completion_tokens, quota,
  avg_use_time, p50_use_time, p95_use_time, p99_use_time, stream_rate, cache_token_rate,
  use_time_sum, stream_count, cache_tokens_total, cache_prompt_tokens, big_input_count, big_input_cache_hits, ttft_count, ttft_sum_ms, ttft_p95_ms, ` + latencyBucketColumnSQL() + `
FROM ` + table + `
WHERE dimension_type = ? AND dimension_key = ? AND bucket_time >= ?
ORDER BY bucket_time ASC`
}

func metricTable(window string) (string, error) {
	switch window {
	case "1m":
		return "metric_1m", nil
	case "5m":
		return "metric_5m", nil
	default:
		return "", fmt.Errorf("unsupported metric window %q", window)
	}
}

func floatPointer(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func int64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func exactOrHistogram(value sql.NullFloat64, buckets latencyhist.Buckets, quantile float64) *float64 {
	if value.Valid {
		return &value.Float64
	}
	return latencyhist.Quantile(buckets, quantile)
}
