CREATE TABLE IF NOT EXISTS nginx_timing_1m (
  instance_id VARCHAR(64) NOT NULL,
  bucket_at DATETIME(3) NOT NULL,
  request_count BIGINT NOT NULL DEFAULT 0,
  upstream_count BIGINT NOT NULL DEFAULT 0,
  status_4xx BIGINT NOT NULL DEFAULT 0,
  status_5xx BIGINT NOT NULL DEFAULT 0,
  status_504 BIGINT NOT NULL DEFAULT 0,
  rt_p50 DOUBLE NOT NULL DEFAULT 0,
  rt_p95 DOUBLE NOT NULL DEFAULT 0,
  rt_max DOUBLE NOT NULL DEFAULT 0,
  uht_p50 DOUBLE NOT NULL DEFAULT 0,
  uht_p95 DOUBLE NOT NULL DEFAULT 0,
  uht_max DOUBLE NOT NULL DEFAULT 0,
  transfer_p50 DOUBLE NOT NULL DEFAULT 0,
  transfer_p95 DOUBLE NOT NULL DEFAULT 0,
  transfer_max DOUBLE NOT NULL DEFAULT 0,
  bytes_total BIGINT NOT NULL DEFAULT 0,
  slow_count BIGINT NOT NULL DEFAULT 0,
  slow_ttft_count BIGINT NOT NULL DEFAULT 0,
  slow_transfer_count BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (instance_id, bucket_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS nginx_slow_samples (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  instance_id VARCHAR(64) NOT NULL,
  occurred_at DATETIME(3) NOT NULL,
  path VARCHAR(500) NOT NULL,
  status INT NOT NULL,
  rt DOUBLE NOT NULL,
  uht DOUBLE NOT NULL,
  urt DOUBLE NOT NULL,
  bytes BIGINT NOT NULL DEFAULT 0,
  UNIQUE KEY uk_nginx_slow_sample (instance_id, occurred_at, path, status),
  INDEX idx_nginx_slow_samples_instance_time (instance_id, occurred_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
