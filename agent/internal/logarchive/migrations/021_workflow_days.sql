CREATE TABLE IF NOT EXISTS archive_workflow_days (
  log_date DATE NOT NULL,
  state VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  revision BIGINT UNSIGNED NOT NULL,
  config_version BIGINT NOT NULL,
  error_code VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (log_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
