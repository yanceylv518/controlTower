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

func (s Store) QueryMetricHistoryPrefix(window, dimensionType, dimensionKeyPrefix string, since time.Time) ([]aggregator.Metric, error) {
	table, err := metricTable(window)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(context.Background(), metricHistoryPrefixSQL(table), dimensionType, dimensionKeyPrefix, since)
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
	types := []string{dimensionType}
	if dimensionType == "" {
		// A parameterized "match any type" predicate defeats the loose index
		// scan on (dimension_type, instance_id, dimension_key, bucket_time),
		// so enumerate the active types first (cheap loose scan) and run one
		// index-friendly equality query per type instead.
		var err error
		if types, err = s.activeDimensionTypes(table, cutoff); err != nil {
			return nil, err
		}
	}
	var out []aggregator.Metric
	for _, t := range types {
		rows, err := s.db.QueryContext(context.Background(), recentMetricsSQL(table, true), cutoff, t, t, limit)
		if err != nil {
			return nil, err
		}
		metrics, err := scanMetrics(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, metrics...)
		if len(out) >= limit {
			out = out[:limit]
			break
		}
	}
	return out, nil
}

func (s Store) activeDimensionTypes(table string, cutoff time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT DISTINCT dimension_type FROM `+table+` WHERE bucket_time >= ?`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanMetrics(rows *sql.Rows) ([]aggregator.Metric, error) {
	var metrics []aggregator.Metric
	for rows.Next() {
		var metric aggregator.Metric
		var successRate, errorRate, avgUseTime, p50UseTime, p95UseTime, p99UseTime, streamRate, cacheTokenRate, ttftP50MS, ttftP90MS, ttftP95MS sql.NullFloat64
		var bigInputCount, bigInputCacheHits, ttftCount, ttftSumMS sql.NullInt64
		var buckets latencyhist.Buckets
		var latencyV2 [latencyhist.BucketCountV2]sql.NullInt64
		var ttftV2 [latencyhist.BucketCountV2]sql.NullInt64
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
			&ttftP50MS,
			&ttftP90MS,
			&ttftP95MS,
		}
		for i := range buckets {
			dest = append(dest, &buckets[i])
		}
		for i := range latencyV2 {
			dest = append(dest, &latencyV2[i])
		}
		for i := range ttftV2 {
			dest = append(dest, &ttftV2[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		metric.LatencyBuckets = buckets
		metric.LatencyBucketsV2 = nullableV2(latencyV2)
		metric.TTFTBuckets = nullableV2(ttftV2)
		metric.SuccessRate = floatPointer(successRate)
		metric.ErrorRate = floatPointer(errorRate)
		metric.AvgUseTime = floatPointer(avgUseTime)
		metric.P50UseTime = quantilePreferV2(metric.LatencyBucketsV2, p50UseTime, buckets, 0.50)
		metric.P95UseTime = quantilePreferV2(metric.LatencyBucketsV2, p95UseTime, buckets, 0.95)
		metric.P99UseTime = quantilePreferV2(metric.LatencyBucketsV2, p99UseTime, buckets, 0.99)
		metric.StreamRate = floatPointer(streamRate)
		metric.CacheTokenRate = floatPointer(cacheTokenRate)
		metric.BigInputCount = int64Pointer(bigInputCount)
		metric.BigInputCacheHits = int64Pointer(bigInputCacheHits)
		metric.TTFTCount = int64Pointer(ttftCount)
		metric.TTFTSumMS = int64Pointer(ttftSumMS)
		metric.TTFTP50MS = ttftQuantile(metric.TTFTBuckets, ttftP50MS, 0.50)
		metric.TTFTP90MS = ttftQuantile(metric.TTFTBuckets, ttftP90MS, 0.90)
		metric.TTFTP95MS = ttftQuantile(metric.TTFTBuckets, ttftP95MS, 0.95)
		metrics = append(metrics, metric)
	}
	return metrics, rows.Err()
}

func nullableV2(values [latencyhist.BucketCountV2]sql.NullInt64) *latencyhist.BucketsV2 {
	var buckets latencyhist.BucketsV2
	for i, value := range values {
		if !value.Valid {
			return nil
		}
		buckets[i] = value.Int64
	}
	return &buckets
}

// quantilePreferV2 keeps the stored exact value when present (unmerged 1m
// rows), then interpolates from the densified V2 histogram, then from V1.
func quantilePreferV2(v2 *latencyhist.BucketsV2, exact sql.NullFloat64, v1 latencyhist.Buckets, q float64) *float64 {
	if exact.Valid {
		return &exact.Float64
	}
	if v2 != nil {
		if value := latencyhist.QuantileV2(*v2, q); value != nil {
			return value
		}
	}
	return latencyhist.Quantile(v1, q)
}

func ttftQuantile(hist *latencyhist.BucketsV2, exact sql.NullFloat64, q float64) *float64 {
	if exact.Valid {
		return &exact.Float64
	}
	if hist != nil {
		if seconds := latencyhist.QuantileV2(*hist, q); seconds != nil {
			value := *seconds * 1000
			return &value
		}
	}
	return nil
}

func recentMetricsSQL(table string, latestOnly bool) string {
	if latestOnly {
		return `SELECT m.instance_id, m.bucket_time, m.dimension_type, m.dimension_key,
  m.request_count, m.success_count, m.error_count, m.success_rate, m.error_rate,
  m.tpm, m.prompt_tokens, m.completion_tokens, m.quota,
  m.avg_use_time, m.p50_use_time, m.p95_use_time, m.p99_use_time, m.stream_rate, m.cache_token_rate,
  m.use_time_sum, m.stream_count, m.cache_tokens_total, m.cache_prompt_tokens, m.big_input_count, m.big_input_cache_hits, m.ttft_count, m.ttft_sum_ms, m.ttft_p50_ms, m.ttft_p90_ms, m.ttft_p95_ms, ` + prefixedLatencyBucketColumnSQL("m") + `, ` + prefixedV2BucketColumnSQL("m") + `
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
  use_time_sum, stream_count, cache_tokens_total, cache_prompt_tokens, big_input_count, big_input_cache_hits, ttft_count, ttft_sum_ms, ttft_p50_ms, ttft_p90_ms, ttft_p95_ms, ` + latencyBucketColumnSQL() + `, ` + v2BucketColumnSQL() + `
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

func prefixedV2BucketColumnSQL(prefix string) string {
	parts := strings.Split(v2BucketColumnSQL(), ", ")
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
  use_time_sum, stream_count, cache_tokens_total, cache_prompt_tokens, big_input_count, big_input_cache_hits, ttft_count, ttft_sum_ms, ttft_p50_ms, ttft_p90_ms, ttft_p95_ms, ` + latencyBucketColumnSQL() + `, ` + v2BucketColumnSQL() + `
FROM ` + table + `
WHERE dimension_type = ? AND dimension_key = ? AND bucket_time >= ?
ORDER BY bucket_time ASC`
}

func metricHistoryPrefixSQL(table string) string {
	return `SELECT instance_id, bucket_time, dimension_type, dimension_key,
  request_count, success_count, error_count, success_rate, error_rate,
  tpm, prompt_tokens, completion_tokens, quota,
  avg_use_time, p50_use_time, p95_use_time, p99_use_time, stream_rate, cache_token_rate,
  use_time_sum, stream_count, cache_tokens_total, cache_prompt_tokens, big_input_count, big_input_cache_hits, ttft_count, ttft_sum_ms, ttft_p50_ms, ttft_p90_ms, ttft_p95_ms, ` + latencyBucketColumnSQL() + `, ` + v2BucketColumnSQL() + `
FROM ` + table + `
WHERE dimension_type = ? AND dimension_key LIKE CONCAT(?, '%') AND bucket_time >= ?
ORDER BY dimension_key ASC, bucket_time ASC`
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
