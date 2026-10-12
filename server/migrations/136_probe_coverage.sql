CREATE TABLE IF NOT EXISTS tuning_routine_probe_coverage (
  instance_id VARCHAR(64) NOT NULL,
  channel_id BIGINT NOT NULL,
  bucket_time DATETIME NOT NULL,
  run_id VARCHAR(32) NOT NULL,
  scheduled BOOLEAN NOT NULL,
  due_count BIGINT NOT NULL,
  sent_count BIGINT NOT NULL,
  global_limited BIGINT NOT NULL,
  channel_limited BIGINT NOT NULL,
  evidence_limited BIGINT NOT NULL,
  last_due_at DATETIME(6) NOT NULL,
  last_sent_at DATETIME(6) NULL,
  last_skipped_at DATETIME(6) NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (instance_id, channel_id, bucket_time, run_id, scheduled),
  KEY idx_probe_coverage_window (instance_id, bucket_time),
  KEY idx_probe_coverage_retention (bucket_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
