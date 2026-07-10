package mysqlstore

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"controltower/server/internal/aggregator"
	"controltower/server/internal/storage"
)

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) Store {
	return Store{db: db}
}

func (s Store) UpsertAgent(agent storage.Agent) error {
	_, err := s.db.ExecContext(context.Background(), `
INSERT INTO agents (
  id, instance_id, version, last_seen_at, last_sequence, last_log_id,
  source_latest_log_id, backlog_estimate, status, report_delay_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  instance_id = VALUES(instance_id),
  version = VALUES(version),
  last_seen_at = VALUES(last_seen_at),
  last_sequence = VALUES(last_sequence),
  last_log_id = GREATEST(last_log_id, VALUES(last_log_id)),
  source_latest_log_id = IF(VALUES(source_latest_log_id) > 0, VALUES(source_latest_log_id), source_latest_log_id),
  backlog_estimate = IF(VALUES(source_latest_log_id) > 0, VALUES(backlog_estimate), backlog_estimate),
  status = VALUES(status),
  report_delay_ms = VALUES(report_delay_ms)`,
		agent.ID,
		agent.InstanceID,
		agent.Version,
		agent.LastSeenAt,
		agent.LastSequence,
		agent.LastLogID,
		agent.SourceLatestLogID,
		agent.BacklogEstimate,
		agent.Status,
		agent.ReportDelayMS,
	)
	return err
}

func (s Store) InsertLogEvent(event storage.LogEvent) (bool, error) {
	result, err := s.db.ExecContext(context.Background(), `
INSERT IGNORE INTO log_events (
  instance_id, source_log_id, created_at, log_type, user_id, username, channel_id, model_name,
  token_id, token_name, prompt_tokens, completion_tokens, total_tokens, quota, use_time,
  is_stream, group_name, request_id, upstream_request_id, error_summary,
  cache_tokens, cache_field_present, inserted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.InstanceID,
		event.SourceLogID,
		event.CreatedAt,
		event.LogType,
		event.UserID,
		event.Username,
		event.ChannelID,
		event.ModelName,
		event.TokenID,
		event.TokenName,
		event.PromptTokens,
		event.CompletionTokens,
		event.TotalTokens,
		event.Quota,
		event.UseTime,
		event.IsStream,
		event.Group,
		event.RequestID,
		event.UpstreamRequestID,
		event.ErrorSummary,
		nullInt64(event.CacheTokens),
		event.CacheFieldPresent,
		time.Now().UTC(),
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s Store) InsertLogSample(sample storage.LogSample) (bool, error) {
	result, err := s.db.ExecContext(context.Background(), `
INSERT IGNORE INTO log_samples (
  instance_id, sample_kind, source_log_id, created_at, log_type, user_id, username, channel_id, model_name,
  token_id, token_name, prompt_tokens, completion_tokens, total_tokens, quota, use_time,
  is_stream, group_name, request_id, upstream_request_id, error_summary,
  cache_tokens, cache_field_present, inserted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sample.InstanceID,
		sample.SampleKind,
		sample.SourceLogID,
		sample.CreatedAt,
		sample.LogType,
		sample.UserID,
		sample.Username,
		sample.ChannelID,
		sample.ModelName,
		sample.TokenID,
		sample.TokenName,
		sample.PromptTokens,
		sample.CompletionTokens,
		sample.TotalTokens,
		sample.Quota,
		sample.UseTime,
		sample.IsStream,
		sample.Group,
		sample.RequestID,
		sample.UpstreamRequestID,
		sample.ErrorSummary,
		nullInt64(sample.CacheTokens),
		sample.CacheFieldPresent,
		time.Now().UTC(),
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}
func (s Store) InsertServerMetric(metric storage.ServerMetric) error {
	_, err := s.db.ExecContext(context.Background(), `
INSERT INTO server_metrics_10s (
  instance_id, collected_at, cpu_percent, memory_used_percent, disk_used_percent,
  network_rx_bytes_per_second, network_tx_bytes_per_second, load_1m
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  cpu_percent = VALUES(cpu_percent),
  memory_used_percent = VALUES(memory_used_percent),
  disk_used_percent = VALUES(disk_used_percent),
  network_rx_bytes_per_second = VALUES(network_rx_bytes_per_second),
  network_tx_bytes_per_second = VALUES(network_tx_bytes_per_second),
  load_1m = VALUES(load_1m)`,
		metric.InstanceID,
		metric.CollectedAt,
		metric.CPUPercent,
		metric.MemoryUsedPercent,
		metric.DiskUsedPercent,
		metric.NetworkRxBytesPerSecond,
		metric.NetworkTxBytesPerSecond,
		metric.Load1m,
	)
	return err
}

func (s Store) InsertDockerStatus(status storage.DockerStatus) error {
	_, err := s.db.ExecContext(context.Background(), `
INSERT INTO docker_statuses (
  instance_id, container_name, collected_at, status, running
) VALUES (?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  status = VALUES(status),
  running = VALUES(running)`,
		status.InstanceID,
		status.ContainerName,
		status.CollectedAt,
		status.Status,
		status.Running,
	)
	return err
}
func (s Store) InsertHealthCheck(check storage.HealthCheck) error {
	_, err := s.db.ExecContext(context.Background(), `
INSERT INTO health_checks (
  instance_id, checked_at, target, status, http_status_code, latency_ms, error_summary
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  status = VALUES(status),
  http_status_code = VALUES(http_status_code),
  latency_ms = VALUES(latency_ms),
  error_summary = VALUES(error_summary)`,
		check.InstanceID,
		check.CheckedAt,
		check.Target,
		check.Status,
		check.HTTPStatusCode,
		check.LatencyMS,
		check.ErrorSummary,
	)
	return err
}

func (s Store) InsertChannelSnapshot(snapshot storage.ChannelSnapshot) error {
	_, err := s.db.ExecContext(context.Background(), `
INSERT INTO channel_snapshots (
  id, instance_id, channel_id, channel_name, status, weight, models_text, captured_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  channel_name = VALUES(channel_name),
  status = VALUES(status),
  weight = VALUES(weight),
  models_text = VALUES(models_text),
  captured_at = VALUES(captured_at)`,
		snapshot.ID,
		snapshot.InstanceID,
		snapshot.ChannelID,
		snapshot.ChannelName,
		snapshot.Status,
		snapshot.Weight,
		snapshot.ModelsText,
		snapshot.CapturedAt,
	)
	return err
}
func (s Store) UpdateLogOffset(instanceID string, lastLogID int64) error {
	_, err := s.db.ExecContext(context.Background(), `
INSERT INTO log_offsets (instance_id, last_log_id, updated_at)
VALUES (?, ?, ?)
ON DUPLICATE KEY UPDATE
  last_log_id = GREATEST(last_log_id, VALUES(last_log_id)),
  updated_at = VALUES(updated_at)`,
		instanceID,
		lastLogID,
		time.Now().UTC(),
	)
	return err
}

func (s Store) CurrentLogOffset(instanceID string) (int64, error) {
	var lastLogID int64
	err := s.db.QueryRowContext(context.Background(), "SELECT COALESCE(MAX(last_log_id), 0) FROM log_offsets WHERE instance_id = ?", instanceID).Scan(&lastLogID)
	return lastLogID, err
}

func (s Store) QueryLogEvents(query storage.LogQuery) ([]storage.LogEvent, error) {
	sqlText, args := buildLogQuery(query)
	rows, err := s.db.QueryContext(context.Background(), sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []storage.LogEvent
	for rows.Next() {
		var event storage.LogEvent
		var cacheTokens sql.NullInt64
		if err := rows.Scan(
			&event.InstanceID,
			&event.SourceLogID,
			&event.CreatedAt,
			&event.LogType,
			&event.UserID,
			&event.Username,
			&event.ChannelID,
			&event.ModelName,
			&event.TokenID,
			&event.TokenName,
			&event.PromptTokens,
			&event.CompletionTokens,
			&event.TotalTokens,
			&event.Quota,
			&event.UseTime,
			&event.IsStream,
			&event.Group,
			&event.RequestID,
			&event.UpstreamRequestID,
			&event.ErrorSummary,
			&cacheTokens,
			&event.CacheFieldPresent,
		); err != nil {
			return nil, err
		}
		if cacheTokens.Valid {
			event.CacheTokens = &cacheTokens.Int64
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s Store) QueryLogSamples(query storage.LogSampleQuery) ([]storage.LogSample, error) {
	sqlText, args := buildLogSampleQuery(query)
	rows, err := s.db.QueryContext(context.Background(), sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var samples []storage.LogSample
	for rows.Next() {
		var sample storage.LogSample
		var cacheTokens sql.NullInt64
		if err := rows.Scan(
			&sample.InstanceID,
			&sample.SampleKind,
			&sample.SourceLogID,
			&sample.CreatedAt,
			&sample.LogType,
			&sample.UserID,
			&sample.Username,
			&sample.ChannelID,
			&sample.ModelName,
			&sample.TokenID,
			&sample.TokenName,
			&sample.PromptTokens,
			&sample.CompletionTokens,
			&sample.TotalTokens,
			&sample.Quota,
			&sample.UseTime,
			&sample.IsStream,
			&sample.Group,
			&sample.RequestID,
			&sample.UpstreamRequestID,
			&sample.ErrorSummary,
			&cacheTokens,
			&sample.CacheFieldPresent,
		); err != nil {
			return nil, err
		}
		if cacheTokens.Valid {
			sample.CacheTokens = &cacheTokens.Int64
		}
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}
func (s Store) EventsForAggregation(ctx context.Context) ([]storage.LogEvent, error) {
	end := time.Now().UTC().Truncate(time.Minute)
	start := end.Add(-10 * time.Minute)
	return s.queryLogEventsContext(ctx, storage.LogQuery{StartTime: start, EndTime: end, Limit: storage.MaxLogQueryLimit})
}

func (s Store) queryLogEventsContext(ctx context.Context, query storage.LogQuery) ([]storage.LogEvent, error) {
	sqlText, args := buildLogQuery(query)
	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []storage.LogEvent
	for rows.Next() {
		var event storage.LogEvent
		var cacheTokens sql.NullInt64
		if err := rows.Scan(
			&event.InstanceID,
			&event.SourceLogID,
			&event.CreatedAt,
			&event.LogType,
			&event.UserID,
			&event.Username,
			&event.ChannelID,
			&event.ModelName,
			&event.TokenID,
			&event.TokenName,
			&event.PromptTokens,
			&event.CompletionTokens,
			&event.TotalTokens,
			&event.Quota,
			&event.UseTime,
			&event.IsStream,
			&event.Group,
			&event.RequestID,
			&event.UpstreamRequestID,
			&event.ErrorSummary,
			&cacheTokens,
			&event.CacheFieldPresent,
		); err != nil {
			return nil, err
		}
		if cacheTokens.Valid {
			event.CacheTokens = &cacheTokens.Int64
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s Store) ApplyMetricBatch(instanceID string, batchID string, metrics []aggregator.Metric) error {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, "INSERT IGNORE INTO metric_batches (instance_id, batch_id, created_at) VALUES (?, ?, ?)", instanceID, batchID, time.Now().UTC())
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		return tx.Commit()
	}

	const batchSize = 100
	rollup5m := aggregator.Rollup5m(metrics)
	for start := 0; start < len(metrics); start += batchSize {
		end := start + batchSize
		if end > len(metrics) {
			end = len(metrics)
		}
		batch := metrics[start:end]
		if _, err := tx.ExecContext(ctx, metricBatchMergeSQL("metric_1m", len(batch)), metricBatchArgs(batch)...); err != nil {
			return err
		}
	}
	for start := 0; start < len(rollup5m); start += batchSize {
		end := start + batchSize
		if end > len(rollup5m) {
			end = len(rollup5m)
		}
		batch := rollup5m[start:end]
		if _, err := tx.ExecContext(ctx, metricBatchMergeSQL("metric_5m", len(batch)), metricBatchArgs(batch)...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s Store) Upsert1m(metrics []aggregator.Metric) error {
	return s.upsertMetrics("metric_1m", metrics)
}

func (s Store) Upsert5m(metrics []aggregator.Metric) error {
	return s.upsertMetrics("metric_5m", metrics)
}

func (s Store) upsertMetrics(table string, metrics []aggregator.Metric) error {
	const batchSize = 100
	for start := 0; start < len(metrics); start += batchSize {
		end := start + batchSize
		if end > len(metrics) {
			end = len(metrics)
		}
		batch := metrics[start:end]
		_, err := s.db.ExecContext(context.Background(), metricBatchUpsertSQL(table, len(batch)), metricBatchArgs(batch)...)
		if err != nil {
			return err
		}
	}
	return nil
}

func buildLogQuery(query storage.LogQuery) (string, []any) {
	builder := strings.Builder{}
	builder.WriteString(`SELECT instance_id, source_log_id, created_at, log_type, user_id, username, channel_id, model_name,
  token_id, token_name, prompt_tokens, completion_tokens, total_tokens, quota, use_time,
  is_stream, group_name, request_id, upstream_request_id, error_summary, cache_tokens, cache_field_present
FROM log_events`)
	var where []string
	var args []any
	if query.InstanceID != "" {
		where = append(where, "instance_id = ?")
		args = append(args, query.InstanceID)
	}
	if query.UserID > 0 {
		where = append(where, "user_id = ?")
		args = append(args, query.UserID)
	}
	if query.ChannelID > 0 {
		where = append(where, "channel_id = ?")
		args = append(args, query.ChannelID)
	}
	if query.ModelName != "" {
		where = append(where, "model_name = ?")
		args = append(args, query.ModelName)
	}
	if query.LogType != "" {
		where = append(where, "log_type = ?")
		args = append(args, query.LogType)
	}
	if query.RequestID != "" {
		where = append(where, "request_id = ?")
		args = append(args, query.RequestID)
	}
	if !query.StartTime.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, query.StartTime)
	}
	if !query.EndTime.IsZero() {
		where = append(where, "created_at <= ?")
		args = append(args, query.EndTime)
	}
	if len(where) > 0 {
		builder.WriteString(" WHERE ")
		builder.WriteString(strings.Join(where, " AND "))
	}
	builder.WriteString(" ORDER BY created_at DESC, source_log_id DESC LIMIT ? OFFSET ?")
	args = append(args, normalizedLimit(query.Limit), normalizedOffset(query.Offset))
	return builder.String(), args
}

func buildLogSampleQuery(query storage.LogSampleQuery) (string, []any) {
	builder := strings.Builder{}
	builder.WriteString(`SELECT instance_id, sample_kind, source_log_id, created_at, log_type, user_id, username, channel_id, model_name,
  token_id, token_name, prompt_tokens, completion_tokens, total_tokens, quota, use_time,
  is_stream, group_name, request_id, upstream_request_id, error_summary, cache_tokens, cache_field_present
FROM log_samples`)
	var where []string
	var args []any
	if query.InstanceID != "" {
		where = append(where, "instance_id = ?")
		args = append(args, query.InstanceID)
	}
	if query.SampleKind != "" {
		where = append(where, "sample_kind = ?")
		args = append(args, query.SampleKind)
	}
	if query.UserID > 0 {
		where = append(where, "user_id = ?")
		args = append(args, query.UserID)
	}
	if query.ChannelID > 0 {
		where = append(where, "channel_id = ?")
		args = append(args, query.ChannelID)
	}
	if query.ModelName != "" {
		where = append(where, "model_name = ?")
		args = append(args, query.ModelName)
	}
	if query.LogType != "" {
		where = append(where, "log_type = ?")
		args = append(args, query.LogType)
	}
	if query.RequestID != "" {
		where = append(where, "request_id = ?")
		args = append(args, query.RequestID)
	}
	if !query.StartTime.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, query.StartTime)
	}
	if !query.EndTime.IsZero() {
		where = append(where, "created_at <= ?")
		args = append(args, query.EndTime)
	}
	if len(where) > 0 {
		builder.WriteString(" WHERE ")
		builder.WriteString(strings.Join(where, " AND "))
	}
	builder.WriteString(" ORDER BY created_at DESC, source_log_id DESC LIMIT ? OFFSET ?")
	args = append(args, normalizedSampleLimit(query.Limit), normalizedOffset(query.Offset))
	return builder.String(), args
}
func normalizedLimit(limit int) int {
	if limit <= 0 || limit > storage.MaxLogQueryLimit {
		return storage.MaxLogQueryLimit
	}
	return limit
}

func normalizedSampleLimit(limit int) int {
	if limit <= 0 || limit > storage.MaxLogSampleQueryLimit {
		return storage.MaxLogSampleQueryLimit
	}
	return limit
}
func normalizedOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

func metricBatchUpsertSQL(table string, rows int) string {
	if rows <= 0 {
		rows = 1
	}
	values := make([]string, 0, rows)
	for i := 0; i < rows; i++ {
		values = append(values, "("+metricValuePlaceholders()+")")
	}
	return `INSERT INTO ` + table + ` (
  instance_id, bucket_time, dimension_type, dimension_key, request_count, success_count, error_count,
  success_rate, error_rate, tpm, prompt_tokens, completion_tokens, quota,
  avg_use_time, p95_use_time, stream_rate, cache_token_rate,
  use_time_sum, stream_count, cache_tokens_total, cache_prompt_tokens, ` + latencyBucketColumnSQL() + `, updated_at
) VALUES ` + strings.Join(values, ", ") + `
ON DUPLICATE KEY UPDATE
  request_count = VALUES(request_count),
  success_count = VALUES(success_count),
  error_count = VALUES(error_count),
  success_rate = VALUES(success_rate),
  error_rate = VALUES(error_rate),
  tpm = VALUES(tpm),
  prompt_tokens = VALUES(prompt_tokens),
  completion_tokens = VALUES(completion_tokens),
  quota = VALUES(quota),
  avg_use_time = VALUES(avg_use_time),
  p95_use_time = VALUES(p95_use_time),
  stream_rate = VALUES(stream_rate),
  cache_token_rate = VALUES(cache_token_rate),
  use_time_sum = VALUES(use_time_sum),
  stream_count = VALUES(stream_count),
  cache_tokens_total = VALUES(cache_tokens_total),
  cache_prompt_tokens = VALUES(cache_prompt_tokens),
  ` + latencyBucketReplaceAssignmentsSQL() + `,
  updated_at = VALUES(updated_at)`
}

func metricBatchMergeSQL(table string, rows int) string {
	sqlText := metricBatchUpsertSQL(table, rows)
	updateAt := strings.Index(sqlText, "ON DUPLICATE KEY UPDATE")
	if updateAt < 0 {
		return sqlText
	}
	return sqlText[:updateAt] + `ON DUPLICATE KEY UPDATE
  success_rate = (success_count + VALUES(success_count)) / NULLIF(request_count + VALUES(request_count), 0),
  error_rate = (error_count + VALUES(error_count)) / NULLIF(request_count + VALUES(request_count), 0),
  avg_use_time = (use_time_sum + VALUES(use_time_sum)) / NULLIF(request_count + VALUES(request_count), 0),
  stream_rate = (stream_count + VALUES(stream_count)) / NULLIF(request_count + VALUES(request_count), 0),
  cache_token_rate = (cache_tokens_total + VALUES(cache_tokens_total)) / NULLIF(cache_prompt_tokens + VALUES(cache_prompt_tokens), 0),
  p95_use_time = ` + latencyP95MergeSQL() + `,
  request_count = request_count + VALUES(request_count),
  success_count = success_count + VALUES(success_count),
  error_count = error_count + VALUES(error_count),
  tpm = tpm + VALUES(tpm),
  prompt_tokens = prompt_tokens + VALUES(prompt_tokens),
  completion_tokens = completion_tokens + VALUES(completion_tokens),
  quota = quota + VALUES(quota),
  use_time_sum = use_time_sum + VALUES(use_time_sum),
  stream_count = stream_count + VALUES(stream_count),
  cache_tokens_total = cache_tokens_total + VALUES(cache_tokens_total),
  cache_prompt_tokens = cache_prompt_tokens + VALUES(cache_prompt_tokens),
  ` + latencyBucketMergeAssignmentsSQL() + `,
  updated_at = VALUES(updated_at)`
}

func metricBatchArgs(metrics []aggregator.Metric) []any {
	args := make([]any, 0, len(metrics)*32)
	for _, metric := range metrics {
		args = append(args, metricArgs(metric)...)
	}
	return args
}
func metricUpsertSQL(table string) string {
	return `INSERT INTO ` + table + ` (
  instance_id, bucket_time, dimension_type, dimension_key, request_count, success_count, error_count,
  success_rate, error_rate, tpm, prompt_tokens, completion_tokens, quota,
  avg_use_time, p95_use_time, stream_rate, cache_token_rate,
  use_time_sum, stream_count, cache_tokens_total, cache_prompt_tokens, ` + latencyBucketColumnSQL() + `, updated_at
) VALUES ` + metricValuePlaceholders() + `
ON DUPLICATE KEY UPDATE
  request_count = VALUES(request_count),
  success_count = VALUES(success_count),
  error_count = VALUES(error_count),
  success_rate = VALUES(success_rate),
  error_rate = VALUES(error_rate),
  tpm = VALUES(tpm),
  prompt_tokens = VALUES(prompt_tokens),
  completion_tokens = VALUES(completion_tokens),
  quota = VALUES(quota),
  avg_use_time = VALUES(avg_use_time),
  p95_use_time = VALUES(p95_use_time),
  stream_rate = VALUES(stream_rate),
  cache_token_rate = VALUES(cache_token_rate),
  use_time_sum = VALUES(use_time_sum),
  stream_count = VALUES(stream_count),
  cache_tokens_total = VALUES(cache_tokens_total),
  cache_prompt_tokens = VALUES(cache_prompt_tokens),
  ` + latencyBucketReplaceAssignmentsSQL() + `,
  updated_at = VALUES(updated_at)`
}

func metricArgs(metric aggregator.Metric) []any {
	args := []any{
		metric.InstanceID,
		metric.BucketTime,
		metric.DimensionType,
		metric.DimensionKey,
		metric.RequestCount,
		metric.SuccessCount,
		metric.ErrorCount,
		nullFloat(metric.SuccessRate),
		nullFloat(metric.ErrorRate),
		metric.TPM,
		metric.PromptTokens,
		metric.CompletionTokens,
		metric.Quota,
		nullFloat(metric.AvgUseTime),
		nullFloat(metric.P95UseTime),
		nullFloat(metric.StreamRate),
		nullFloat(metric.CacheTokenRate),
		metric.UseTimeSum,
		metric.StreamCount,
		metric.CacheTokensTotal,
		metric.CachePromptTokens,
	}
	for _, bucket := range metric.LatencyBuckets {
		args = append(args, bucket)
	}
	return append(args, time.Now().UTC())
}

func nullTime(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}
func nullInt64(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func nullFloat(value *float64) sql.NullFloat64 {
	if value == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *value, Valid: true}
}
