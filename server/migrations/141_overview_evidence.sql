CREATE TABLE IF NOT EXISTS overview_metric_coverage (
  instance_id VARCHAR(64) NOT NULL,
  bucket_time DATETIME(6) NOT NULL,
  confirmed_at DATETIME(6) NOT NULL,
  PRIMARY KEY (instance_id, bucket_time),
  KEY idx_overview_coverage_expiry (bucket_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS overview_alert_evaluation (
  id TINYINT NOT NULL PRIMARY KEY,
  completed_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS overview_metric_sources (
  instance_id VARCHAR(64) NOT NULL PRIMARY KEY,
  last_marker_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE alerts ADD COLUMN confirmed_at DATETIME(6) NULL;
ALTER TABLE alerts ADD COLUMN dimension_type VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE alerts ADD COLUMN dimension_key VARCHAR(255) NOT NULL DEFAULT '';

CREATE INDEX idx_health_latest_target ON health_checks(instance_id,target,checked_at);
CREATE INDEX idx_docker_latest_container ON docker_statuses(instance_id,container_name,collected_at);
