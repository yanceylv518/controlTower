CREATE TABLE IF NOT EXISTS billing_user_settings (
  instance_id VARCHAR(64) NOT NULL,
  user_id BIGINT NOT NULL,
  use_tiered_pricing TINYINT(1) NOT NULL DEFAULT 1,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(128) NOT NULL DEFAULT '',
  PRIMARY KEY (instance_id, user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_jobs (
  id VARCHAR(40) NOT NULL,
  instance_id VARCHAR(64) NOT NULL,
  job_type VARCHAR(24) NOT NULL DEFAULT 'generate',
  user_id BIGINT NOT NULL DEFAULT 0,
  range_from DATETIME NOT NULL,
  range_to DATETIME NOT NULL,
  status VARCHAR(24) NOT NULL DEFAULT 'pending',
  total_steps INT NOT NULL DEFAULT 0,
  completed_steps INT NOT NULL DEFAULT 0,
  abnormal_rows BIGINT NOT NULL DEFAULT 0,
  error_message VARCHAR(1000) NOT NULL DEFAULT '',
  output_path VARCHAR(1000) NOT NULL DEFAULT '',
  requested_by VARCHAR(128) NOT NULL DEFAULT '',
  created_at DATETIME(6) NOT NULL,
  started_at DATETIME(6) NULL,
  finished_at DATETIME(6) NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_billing_jobs_claim (status, created_at),
  KEY idx_billing_jobs_instance (instance_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_job_steps (
  job_id VARCHAR(40) NOT NULL,
  step_no INT NOT NULL,
  range_from DATETIME NOT NULL,
  range_to DATETIME NOT NULL,
  status VARCHAR(24) NOT NULL DEFAULT 'pending',
  cursor_created_at BIGINT NOT NULL DEFAULT 0,
  cursor_id BIGINT NOT NULL DEFAULT 0,
  processed_rows BIGINT NOT NULL DEFAULT 0,
  abnormal_rows BIGINT NOT NULL DEFAULT 0,
  attempts INT NOT NULL DEFAULT 0,
  error_message VARCHAR(1000) NOT NULL DEFAULT '',
  started_at DATETIME(6) NULL,
  finished_at DATETIME(6) NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (job_id, step_no),
  KEY idx_billing_steps_claim (status, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_hourly (
  job_id VARCHAR(40) NOT NULL,
  instance_id VARCHAR(64) NOT NULL,
  hour_start DATETIME NOT NULL,
  user_id BIGINT NOT NULL,
  username VARCHAR(128) NOT NULL DEFAULT '',
  model_name VARCHAR(255) NOT NULL,
  group_name VARCHAR(64) NOT NULL DEFAULT '',
  tier_from BIGINT NOT NULL DEFAULT 0,
  request_count BIGINT NOT NULL DEFAULT 0,
  prompt_tokens BIGINT NOT NULL DEFAULT 0,
  completion_tokens BIGINT NOT NULL DEFAULT 0,
  cache_tokens BIGINT NOT NULL DEFAULT 0,
  quota BIGINT NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (job_id, hour_start, user_id, model_name, group_name, tier_from),
  KEY idx_billing_hourly_finalize (instance_id, hour_start)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_anomaly_orders (
  instance_id VARCHAR(64) NOT NULL,
  source_log_id BIGINT NOT NULL,
  job_id VARCHAR(40) NOT NULL,
  created_at DATETIME NOT NULL,
  request_id VARCHAR(128) NOT NULL DEFAULT '',
  user_id BIGINT NOT NULL DEFAULT 0,
  username VARCHAR(128) NOT NULL DEFAULT '',
  model_name VARCHAR(255) NOT NULL DEFAULT '',
  group_name VARCHAR(64) NOT NULL DEFAULT '',
  prompt_tokens BIGINT NULL,
  completion_tokens BIGINT NULL,
  cache_tokens BIGINT NOT NULL DEFAULT 0,
  quota BIGINT NOT NULL DEFAULT 0,
  max_context_tokens BIGINT NOT NULL DEFAULT 0,
  reasons VARCHAR(255) NOT NULL,
  detected_at DATETIME(6) NOT NULL,
  PRIMARY KEY (instance_id, source_log_id),
  KEY idx_billing_anomaly_user_time (instance_id, user_id, created_at, source_log_id),
  KEY idx_billing_anomaly_time (instance_id, created_at, source_log_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_daily_versions (
  job_id VARCHAR(40) NOT NULL,
  instance_id VARCHAR(64) NOT NULL,
  user_id BIGINT NOT NULL,
  username VARCHAR(128) NOT NULL DEFAULT '',
  model_name VARCHAR(255) NOT NULL,
  group_name VARCHAR(64) NOT NULL DEFAULT '',
  tier_from BIGINT NOT NULL DEFAULT 0,
  day DATE NOT NULL,
  request_count BIGINT NOT NULL DEFAULT 0,
  prompt_tokens BIGINT NOT NULL DEFAULT 0,
  completion_tokens BIGINT NOT NULL DEFAULT 0,
  cache_tokens BIGINT NOT NULL DEFAULT 0,
  quota BIGINT NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (job_id, user_id, model_name, group_name, tier_from, day),
  KEY idx_billing_versions_report (instance_id, day, user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_active_versions (
  instance_id VARCHAR(64) NOT NULL,
  day DATE NOT NULL,
  job_id VARCHAR(40) NOT NULL,
  activated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (instance_id, day),
  KEY idx_billing_active_job (job_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
