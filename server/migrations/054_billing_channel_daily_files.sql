CREATE TABLE IF NOT EXISTS billing_channel_daily_files (
  job_id VARCHAR(40) NOT NULL,
  instance_id VARCHAR(64) NOT NULL,
  bill_day DATE NOT NULL,
  channel_id BIGINT NOT NULL,
  relative_path VARCHAR(512) NOT NULL,
  file_size BIGINT NOT NULL,
  sha256 CHAR(64) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (job_id, bill_day, channel_id),
  KEY idx_billing_channel_file_lookup (instance_id, bill_day, channel_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
