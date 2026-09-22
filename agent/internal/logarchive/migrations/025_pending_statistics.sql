CREATE TABLE IF NOT EXISTS archive_pending_statistics (
  log_date DATE NOT NULL,
  source_id BIGINT NOT NULL,
  PRIMARY KEY (log_date,source_id),
  KEY idx_pending_source (source_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
