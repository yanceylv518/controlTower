CREATE TABLE IF NOT EXISTS billing_job_user_settings (
  job_id VARCHAR(40) NOT NULL,
  user_id BIGINT NOT NULL,
  use_tiered_pricing TINYINT(1) NOT NULL DEFAULT 1,
  PRIMARY KEY (job_id, user_id),
  KEY idx_billing_job_user_settings_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
