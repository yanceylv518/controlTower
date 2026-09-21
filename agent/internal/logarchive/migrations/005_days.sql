CREATE TABLE IF NOT EXISTS archive_days (
  log_date DATE NOT NULL,
  mutation_revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
  state VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  current_version_id BINARY(16) NULL,
  latest_version_no INT UNSIGNED NOT NULL DEFAULT 0,
  last_reconcile_run_id BINARY(16) NULL,
  freeze_task_id BINARY(16) NULL,
  freeze_epoch BIGINT UNSIGNED NULL,
  freeze_revision BIGINT UNSIGNED NULL,
  freeze_until DATETIME(6) NULL,
  blocking_issue_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
  catalog_revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (log_date),
  KEY idx_archive_days_state (state, log_date),
  CONSTRAINT fk_archive_current_day_version FOREIGN KEY (log_date, current_version_id)
    REFERENCES archive_day_versions (log_date, day_version_id) ON DELETE RESTRICT ON UPDATE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
