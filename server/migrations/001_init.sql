CREATE TABLE IF NOT EXISTS instances (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  name VARCHAR(128) NOT NULL,
  env VARCHAR(64) NOT NULL,
  region VARCHAR(64) NOT NULL,
  base_url VARCHAR(512) NOT NULL,
  enabled TINYINT(1) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS agents (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  instance_id VARCHAR(64) NOT NULL,
  version VARCHAR(64) NOT NULL,
  last_seen_at DATETIME(6) NOT NULL,
  last_sequence BIGINT NOT NULL,
  last_log_id BIGINT NOT NULL,
  source_latest_log_id BIGINT NOT NULL DEFAULT 0,
  backlog_estimate BIGINT NOT NULL DEFAULT 0,
  status VARCHAR(32) NOT NULL,
  report_delay_ms BIGINT NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_agents_instance ON agents (instance_id);

CREATE TABLE IF NOT EXISTS log_offsets (
  instance_id VARCHAR(64) NOT NULL PRIMARY KEY,
  last_log_id BIGINT NOT NULL,
  updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS log_events (
  instance_id VARCHAR(64) NOT NULL,
  source_log_id BIGINT NOT NULL,
  created_at DATETIME(6) NOT NULL,
  log_type VARCHAR(32) NOT NULL,
  user_id BIGINT NOT NULL,
  username VARCHAR(255) NOT NULL,
  channel_id BIGINT NOT NULL,
  model_name VARCHAR(255) NOT NULL,
  token_id BIGINT NOT NULL,
  token_name VARCHAR(255) NOT NULL,
  prompt_tokens BIGINT NOT NULL,
  completion_tokens BIGINT NOT NULL,
  total_tokens BIGINT NOT NULL,
  quota BIGINT NOT NULL,
  use_time DOUBLE NOT NULL,
  is_stream TINYINT(1) NOT NULL,
  group_name VARCHAR(128) NOT NULL,
  request_id VARCHAR(255) NOT NULL,
  upstream_request_id VARCHAR(255) NOT NULL,
  error_summary VARCHAR(300) NOT NULL,
  cache_tokens BIGINT,
  cache_field_present TINYINT(1) NOT NULL,
  inserted_at DATETIME(6) NOT NULL,
  PRIMARY KEY (instance_id, source_log_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_log_events_instance_created ON log_events (instance_id, created_at);
CREATE INDEX idx_log_events_channel_created ON log_events (instance_id, channel_id, created_at);
CREATE INDEX idx_log_events_model_created ON log_events (instance_id, model_name, created_at);
CREATE INDEX idx_log_events_user_created ON log_events (instance_id, user_id, created_at);
CREATE INDEX idx_log_events_request_id ON log_events (request_id);


CREATE TABLE IF NOT EXISTS log_samples (
  instance_id VARCHAR(64) NOT NULL,
  sample_kind VARCHAR(32) NOT NULL,
  source_log_id BIGINT NOT NULL,
  created_at DATETIME(6) NOT NULL,
  log_type VARCHAR(32) NOT NULL,
  user_id BIGINT NOT NULL,
  username VARCHAR(255) NOT NULL,
  channel_id BIGINT NOT NULL,
  model_name VARCHAR(255) NOT NULL,
  token_id BIGINT NOT NULL,
  token_name VARCHAR(255) NOT NULL,
  prompt_tokens BIGINT NOT NULL,
  completion_tokens BIGINT NOT NULL,
  total_tokens BIGINT NOT NULL,
  quota BIGINT NOT NULL,
  use_time DOUBLE NOT NULL,
  is_stream TINYINT(1) NOT NULL,
  group_name VARCHAR(128) NOT NULL,
  request_id VARCHAR(255) NOT NULL,
  upstream_request_id VARCHAR(255) NOT NULL,
  error_summary VARCHAR(300) NOT NULL,
  cache_tokens BIGINT,
  cache_field_present TINYINT(1) NOT NULL,
  inserted_at DATETIME(6) NOT NULL,
  PRIMARY KEY (instance_id, sample_kind, source_log_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_log_samples_instance_created ON log_samples (instance_id, created_at);
CREATE INDEX idx_log_samples_kind_created ON log_samples (sample_kind, created_at);
CREATE INDEX idx_log_samples_request_id ON log_samples (request_id);
CREATE TABLE IF NOT EXISTS server_metrics_10s (
  instance_id VARCHAR(64) NOT NULL,
  collected_at DATETIME(6) NOT NULL,
  cpu_percent DOUBLE NOT NULL,
  memory_used_percent DOUBLE NOT NULL,
  disk_used_percent DOUBLE NOT NULL,
  network_rx_bytes_per_second BIGINT NOT NULL,
  network_tx_bytes_per_second BIGINT NOT NULL,
  load_1m DOUBLE NOT NULL,
  PRIMARY KEY (instance_id, collected_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;



CREATE TABLE IF NOT EXISTS docker_statuses (
  instance_id VARCHAR(64) NOT NULL,
  container_name VARCHAR(255) NOT NULL,
  collected_at DATETIME(6) NOT NULL,
  status VARCHAR(255) NOT NULL,
  running TINYINT(1) NOT NULL,
  PRIMARY KEY (instance_id, container_name, collected_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_docker_statuses_instance_collected ON docker_statuses (instance_id, collected_at);
CREATE TABLE IF NOT EXISTS health_checks (
  instance_id VARCHAR(64) NOT NULL,
  checked_at DATETIME(6) NOT NULL,
  target VARCHAR(512) NOT NULL,
  status VARCHAR(32) NOT NULL,
  http_status_code INT NOT NULL,
  latency_ms BIGINT NOT NULL,
  error_summary VARCHAR(300) NOT NULL,
  PRIMARY KEY (instance_id, target, checked_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_health_checks_instance_checked ON health_checks (instance_id, checked_at);
CREATE TABLE IF NOT EXISTS metric_1m (
  instance_id VARCHAR(64) NOT NULL,
  bucket_time DATETIME(6) NOT NULL,
  dimension_type VARCHAR(64) NOT NULL,
  dimension_key VARCHAR(512) NOT NULL,
  request_count BIGINT NOT NULL,
  success_count BIGINT NOT NULL,
  error_count BIGINT NOT NULL,
  success_rate DOUBLE,
  error_rate DOUBLE,
  tpm BIGINT NOT NULL,
  prompt_tokens BIGINT NOT NULL,
  completion_tokens BIGINT NOT NULL,
  quota BIGINT NOT NULL,
  avg_use_time DOUBLE,
  p95_use_time DOUBLE,
  latency_le_250ms BIGINT NOT NULL DEFAULT 0,
  latency_le_500ms BIGINT NOT NULL DEFAULT 0,
  latency_le_1s BIGINT NOT NULL DEFAULT 0,
  latency_le_2s BIGINT NOT NULL DEFAULT 0,
  latency_le_3s BIGINT NOT NULL DEFAULT 0,
  latency_le_5s BIGINT NOT NULL DEFAULT 0,
  latency_le_10s BIGINT NOT NULL DEFAULT 0,
  latency_le_30s BIGINT NOT NULL DEFAULT 0,
  latency_le_60s BIGINT NOT NULL DEFAULT 0,
  latency_gt_60s BIGINT NOT NULL DEFAULT 0,
  stream_rate DOUBLE,
  cache_token_rate DOUBLE,
  use_time_sum DOUBLE NOT NULL DEFAULT 0,
  stream_count BIGINT NOT NULL DEFAULT 0,
  cache_tokens_total BIGINT NOT NULL DEFAULT 0,
  cache_prompt_tokens BIGINT NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (instance_id, bucket_time, dimension_type, dimension_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_metric_1m_bucket_dimension ON metric_1m (bucket_time, dimension_type, dimension_key);

CREATE TABLE IF NOT EXISTS metric_batches (
  instance_id VARCHAR(64) NOT NULL,
  batch_id VARCHAR(160) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (instance_id, batch_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_metric_batches_created ON metric_batches (created_at);

CREATE TABLE IF NOT EXISTS metric_5m (
  instance_id VARCHAR(64) NOT NULL,
  bucket_time DATETIME(6) NOT NULL,
  dimension_type VARCHAR(64) NOT NULL,
  dimension_key VARCHAR(512) NOT NULL,
  request_count BIGINT NOT NULL,
  success_count BIGINT NOT NULL,
  error_count BIGINT NOT NULL,
  success_rate DOUBLE,
  error_rate DOUBLE,
  tpm BIGINT NOT NULL,
  prompt_tokens BIGINT NOT NULL,
  completion_tokens BIGINT NOT NULL,
  quota BIGINT NOT NULL,
  avg_use_time DOUBLE,
  p95_use_time DOUBLE,
  latency_le_250ms BIGINT NOT NULL DEFAULT 0,
  latency_le_500ms BIGINT NOT NULL DEFAULT 0,
  latency_le_1s BIGINT NOT NULL DEFAULT 0,
  latency_le_2s BIGINT NOT NULL DEFAULT 0,
  latency_le_3s BIGINT NOT NULL DEFAULT 0,
  latency_le_5s BIGINT NOT NULL DEFAULT 0,
  latency_le_10s BIGINT NOT NULL DEFAULT 0,
  latency_le_30s BIGINT NOT NULL DEFAULT 0,
  latency_le_60s BIGINT NOT NULL DEFAULT 0,
  latency_gt_60s BIGINT NOT NULL DEFAULT 0,
  stream_rate DOUBLE,
  cache_token_rate DOUBLE,
  use_time_sum DOUBLE NOT NULL DEFAULT 0,
  stream_count BIGINT NOT NULL DEFAULT 0,
  cache_tokens_total BIGINT NOT NULL DEFAULT 0,
  cache_prompt_tokens BIGINT NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (instance_id, bucket_time, dimension_type, dimension_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_metric_5m_bucket_dimension ON metric_5m (bucket_time, dimension_type, dimension_key);

CREATE TABLE IF NOT EXISTS alerts (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  instance_id VARCHAR(64) NOT NULL,
  rule_key VARCHAR(128) NOT NULL,
  severity VARCHAR(32) NOT NULL,
  status VARCHAR(32) NOT NULL,
  title VARCHAR(255) NOT NULL,
  summary TEXT NOT NULL,
  first_seen_at DATETIME(6) NOT NULL,
  last_seen_at DATETIME(6) NOT NULL,
  resolved_at DATETIME(6),
  silence_until DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_alerts_status_severity ON alerts (status, severity);
CREATE INDEX idx_alerts_instance_rule ON alerts (instance_id, rule_key);

CREATE TABLE IF NOT EXISTS notification_channels (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  channel_type VARCHAR(32) NOT NULL,
  name VARCHAR(128) NOT NULL,
  webhook_url TEXT NOT NULL,
  secret_value TEXT NOT NULL,
  enabled TINYINT(1) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;


CREATE TABLE IF NOT EXISTS notification_deliveries (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  alert_id VARCHAR(64) NOT NULL,
  channel_id VARCHAR(64) NOT NULL,
  status VARCHAR(32) NOT NULL,
  attempted_at DATETIME(6) NOT NULL,
  next_attempt_at DATETIME(6) NOT NULL,
  attempts INT NOT NULL,
  status_code INT NOT NULL,
  error_summary VARCHAR(300) NOT NULL,
  UNIQUE KEY uniq_notification_delivery_alert_channel (alert_id, channel_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_notification_deliveries_attempted ON notification_deliveries (attempted_at);
CREATE TABLE IF NOT EXISTS operation_audits (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  instance_id VARCHAR(64) NOT NULL,
  operation_type VARCHAR(64) NOT NULL,
  target_type VARCHAR(64) NOT NULL,
  target_id VARCHAR(128) NOT NULL,
  actor_id VARCHAR(128) NOT NULL,
  before_summary TEXT NOT NULL,
  after_summary TEXT NOT NULL,
  status VARCHAR(32) NOT NULL,
  created_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_operation_audits_instance_created ON operation_audits (instance_id, created_at);

CREATE TABLE IF NOT EXISTS channel_snapshots (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  instance_id VARCHAR(64) NOT NULL,
  channel_id BIGINT NOT NULL,
  channel_name VARCHAR(255) NOT NULL,
  status VARCHAR(64) NOT NULL,
  weight BIGINT NOT NULL,
  models_text TEXT NOT NULL,
  captured_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_channel_snapshots_instance_channel ON channel_snapshots (instance_id, channel_id, captured_at);
CREATE INDEX idx_channel_snapshots_captured ON channel_snapshots (captured_at);

CREATE TABLE IF NOT EXISTS weight_adjustments (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  instance_id VARCHAR(64) NOT NULL,
  channel_id BIGINT NOT NULL,
  mode VARCHAR(32) NOT NULL,
  previous_weight BIGINT NOT NULL,
  suggested_weight BIGINT NOT NULL,
  applied_weight BIGINT,
  reason TEXT NOT NULL,
  status VARCHAR(32) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  applied_at DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_weight_adjustments_instance_channel ON weight_adjustments (instance_id, channel_id, created_at);





ALTER TABLE notification_deliveries ADD COLUMN next_attempt_at DATETIME(6) NOT NULL DEFAULT '1970-01-01 00:00:00';
ALTER TABLE agents ADD COLUMN source_latest_log_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE agents ADD COLUMN backlog_estimate BIGINT NOT NULL DEFAULT 0;
ALTER TABLE notification_deliveries ADD COLUMN attempts INT NOT NULL DEFAULT 0;
CREATE INDEX idx_notification_deliveries_next_attempt ON notification_deliveries (status, next_attempt_at);
ALTER TABLE metric_1m ADD COLUMN use_time_sum DOUBLE NOT NULL DEFAULT 0;
ALTER TABLE metric_1m ADD COLUMN stream_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_1m ADD COLUMN cache_tokens_total BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_1m ADD COLUMN cache_prompt_tokens BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_5m ADD COLUMN use_time_sum DOUBLE NOT NULL DEFAULT 0;
ALTER TABLE metric_5m ADD COLUMN stream_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_5m ADD COLUMN cache_tokens_total BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_5m ADD COLUMN cache_prompt_tokens BIGINT NOT NULL DEFAULT 0;

ALTER TABLE metric_1m ADD COLUMN latency_le_250ms BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_1m ADD COLUMN latency_le_500ms BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_1m ADD COLUMN latency_le_1s BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_1m ADD COLUMN latency_le_2s BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_1m ADD COLUMN latency_le_3s BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_1m ADD COLUMN latency_le_5s BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_1m ADD COLUMN latency_le_10s BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_1m ADD COLUMN latency_le_30s BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_1m ADD COLUMN latency_le_60s BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_1m ADD COLUMN latency_gt_60s BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_5m ADD COLUMN latency_le_250ms BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_5m ADD COLUMN latency_le_500ms BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_5m ADD COLUMN latency_le_1s BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_5m ADD COLUMN latency_le_2s BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_5m ADD COLUMN latency_le_3s BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_5m ADD COLUMN latency_le_5s BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_5m ADD COLUMN latency_le_10s BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_5m ADD COLUMN latency_le_30s BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_5m ADD COLUMN latency_le_60s BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_5m ADD COLUMN latency_gt_60s BIGINT NOT NULL DEFAULT 0;