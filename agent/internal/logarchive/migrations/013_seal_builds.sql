CREATE TABLE IF NOT EXISTS archive_seal_builds (
  build_id BINARY(16) NOT NULL,
  task_id BINARY(16) NOT NULL,
  attempt INT UNSIGNED NOT NULL,
  task_json JSON NOT NULL,
  state VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  writer_epoch BIGINT UNSIGNED NOT NULL,
  progress_version BIGINT UNSIGNED NOT NULL DEFAULT 0,
  progress_json JSON NOT NULL,
  error_code VARCHAR(48) CHARACTER SET ascii COLLATE ascii_bin NULL,
  catalog_revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  published_at DATETIME(6) NULL,
  PRIMARY KEY (build_id),
  UNIQUE KEY uq_archive_seal_task (task_id, attempt)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
