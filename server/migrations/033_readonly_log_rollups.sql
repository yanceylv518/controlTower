CREATE TABLE IF NOT EXISTS readonly_log_rollup_cursors (
  site_id VARCHAR(64) NOT NULL,
  last_log_id BIGINT NOT NULL DEFAULT 0,
  initialized TINYINT(1) NOT NULL DEFAULT 0,
  coverage_from DATETIME(6) NULL,
  last_synced_at DATETIME(6) NULL,
  caught_up_at DATETIME(6) NULL,
  last_error TEXT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (site_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS readonly_log_stats_hourly (
  dimension_hash BINARY(32) NOT NULL,
  site_id VARCHAR(64) NOT NULL,
  hour_start DATETIME NOT NULL,
  log_type INT NOT NULL,
  user_id BIGINT NOT NULL DEFAULT 0,
  username VARCHAR(255) NOT NULL DEFAULT '',
  channel_id BIGINT NOT NULL DEFAULT 0,
  model_name VARCHAR(255) NOT NULL DEFAULT '',
  token_name VARCHAR(255) NOT NULL DEFAULT '',
  group_name VARCHAR(64) NOT NULL DEFAULT '',
  request_count BIGINT NOT NULL DEFAULT 0,
  prompt_tokens BIGINT NOT NULL DEFAULT 0,
  completion_tokens BIGINT NOT NULL DEFAULT 0,
  quota_sum BIGINT NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (dimension_hash),
  KEY idx_readonly_log_stats_site_hour (site_id, hour_start),
  KEY idx_readonly_log_stats_site_user_hour (site_id, user_id, hour_start),
  KEY idx_readonly_log_stats_site_model_hour (site_id, model_name, hour_start)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
