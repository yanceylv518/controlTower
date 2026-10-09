CREATE TABLE IF NOT EXISTS alb_access_log_config (
  id TINYINT NOT NULL,
  config_json LONGTEXT NOT NULL,
  secret_cipher TEXT NOT NULL,
  version BIGINT NOT NULL,
  updated_by VARCHAR(191) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
