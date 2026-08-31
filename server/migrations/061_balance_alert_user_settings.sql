CREATE TABLE IF NOT EXISTS balance_alert_user_settings (
  instance_id VARCHAR(64) NOT NULL,
  user_id BIGINT NOT NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(128) NOT NULL DEFAULT '',
  PRIMARY KEY (instance_id, user_id),
  KEY idx_balance_alert_enabled (instance_id, enabled, user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
