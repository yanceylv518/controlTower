ALTER TABLE tuning_continuous_states ADD COLUMN probe_speed_json JSON NULL;

CREATE TABLE IF NOT EXISTS tuning_routine_probe_minutes (
  instance_id VARCHAR(64) NOT NULL,
  channel_id BIGINT NOT NULL,
  model_name VARCHAR(255) NOT NULL,
  control_revision BIGINT NOT NULL DEFAULT 0,
  base_updated_at DATETIME(6) NOT NULL,
  bucket_time DATETIME NOT NULL,
  sample_count BIGINT NOT NULL,
  duration_seconds DOUBLE NOT NULL,
  last_started_at DATETIME(6) NOT NULL,
  PRIMARY KEY (instance_id, channel_id, bucket_time, control_revision, base_updated_at, model_name),
  KEY idx_probe_retention (bucket_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
