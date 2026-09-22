CREATE TABLE IF NOT EXISTS archive_pipeline (
  singleton_id TINYINT UNSIGNED NOT NULL,
  state_json JSON NOT NULL,
  writer_epoch BIGINT UNSIGNED NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (singleton_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
