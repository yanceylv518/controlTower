CREATE TABLE IF NOT EXISTS tuning_probe_records (
  id VARCHAR(32) NOT NULL PRIMARY KEY,
  instance_id VARCHAR(64) NOT NULL,
  channel_id BIGINT NOT NULL,
  channel_name VARCHAR(255) NOT NULL,
  model_name VARCHAR(255) NOT NULL,
  kind VARCHAR(20) NOT NULL,
  started_at DATETIME(6) NOT NULL,
  deadline_at DATETIME(6) NOT NULL,
  finished_at DATETIME(6) NULL,
  outcome VARCHAR(20) NOT NULL,
  reason VARCHAR(40) NOT NULL DEFAULT '',
  duration_seconds DOUBLE NULL,
  KEY idx_probe_records_site_time (instance_id,started_at,id),
  KEY idx_probe_records_channel_time (instance_id,channel_id,started_at,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tuning_probe_recorders (
  instance_id VARCHAR(64) NOT NULL,
  run_id VARCHAR(32) NOT NULL,
  started_at DATETIME(6) NOT NULL,
  last_seen_at DATETIME(6) NOT NULL,
  last_loss_at DATETIME(6) NULL,
  dropped_records BIGINT NOT NULL,
  queued_records BIGINT NOT NULL,
  PRIMARY KEY (instance_id,run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS channel_control_requests (
  id VARCHAR(32) NOT NULL PRIMARY KEY,
  instance_id VARCHAR(64) NOT NULL,
  channel_id BIGINT NOT NULL,
  operation VARCHAR(32) NOT NULL,
  payload_json JSON NOT NULL,
  actor VARCHAR(128) NOT NULL,
  recommendation_id VARCHAR(64) NOT NULL DEFAULT '',
  command_id VARCHAR(64) NOT NULL DEFAULT '',
  started_at DATETIME(6) NOT NULL,
  finished_at DATETIME(6) NULL,
  outcome VARCHAR(20) NOT NULL,
  reason VARCHAR(64) NOT NULL DEFAULT '',
  duration_seconds DOUBLE NULL,
  KEY idx_control_channel_time (instance_id,channel_id,started_at,id),
  KEY idx_control_site_time (instance_id,started_at),
  KEY idx_control_command (command_id),
  KEY idx_control_recommendation (recommendation_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_channel_commands_lifeline ON channel_commands (instance_id,channel_id,created_at,id);
CREATE INDEX idx_tuning_recommendations_command ON tuning_recommendations (command_id);
