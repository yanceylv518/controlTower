package mysqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"controltower/internal/latencyhist"
	"controltower/server/internal/aggregator"
)

func (s Store) Recent1mMetrics() ([]aggregator.Metric, error) {
	return s.recentMetrics("metric_1m", 200, false)
}

func (s Store) Latest1mMetrics() ([]aggregator.Metric, error) {
	return s.recentMetrics("metric_1m", 5000, true)
}

func (s Store) Recent5mMetrics() ([]aggregator.Metric, error) {
	return s.recentMetrics("metric_5m", 200, false)
}

func (s Store) Latest5mMetrics() ([]aggregator.Metric, error) {
	return s.recentMetrics("metric_5m", 5000, true)
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

func scanMetrics(rows *sql.Rows) ([]aggregator.Metric, error) {
	var metrics []aggregator.Metric
	for rows.Next() {
		var metric aggregator.Metric
		var successRate, errorRate, avgUseTime, p95UseTime, streamRate, cacheTokenRate sql.NullFloat64
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
			&p95UseTime,
			&streamRate,
			&cacheTokenRate,
			&metric.UseTimeSum,
			&metric.StreamCount,
			&metric.CacheTokensTotal,
			&metric.CachePromptTokens,
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
		metric.P95UseTime = floatPointer(p95UseTime)
		metric.StreamRate = floatPointer(streamRate)
		metric.CacheTokenRate = floatPointer(cacheTokenRate)
		metrics = append(metrics, metric)
	}
	return metrics, rows.Err()
}

func recentMetricsSQL(table string, latestOnly bool) string {
	where := ""
	if latestOnly {
		where = ` AS current WHERE bucket_time = (SELECT MAX(candidate.bucket_time) FROM ` + table + ` AS candidate
  WHERE candidate.instance_id = current.instance_id
    AND candidate.dimension_type = current.dimension_type
    AND candidate.dimension_key = current.dimension_key)`
	}
	return `SELECT instance_id, bucket_time, dimension_type, dimension_key,
  request_count, success_count, error_count, success_rate, error_rate,
  tpm, prompt_tokens, completion_tokens, quota,
  avg_use_time, p95_use_time, stream_rate, cache_token_rate,
  use_time_sum, stream_count, cache_tokens_total, cache_prompt_tokens, ` + latencyBucketColumnSQL() + `
FROM ` + table + where + `
ORDER BY bucket_time DESC, dimension_type ASC, dimension_key ASC
LIMIT ?`
}

func metricHistorySQL(table string) string {
	return `SELECT instance_id, bucket_time, dimension_type, dimension_key,
  request_count, success_count, error_count, success_rate, error_rate,
  tpm, prompt_tokens, completion_tokens, quota,
  avg_use_time, p95_use_time, stream_rate, cache_token_rate,
  use_time_sum, stream_count, cache_tokens_total, cache_prompt_tokens, ` + latencyBucketColumnSQL() + `
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
