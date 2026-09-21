CREATE TABLE IF NOT EXISTS archive_ingest_issues (
  stream_key VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  source_id BIGINT NOT NULL,
  log_date DATE NULL,
  error_code VARCHAR(48) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  row_bytes BIGINT UNSIGNED NOT NULL,
  resolved_at DATETIME(6) NULL,
  first_seen_at DATETIME(6) NOT NULL,
  last_seen_at DATETIME(6) NOT NULL,
  PRIMARY KEY (stream_key, source_id),
  KEY idx_archive_issue_source (source_id, resolved_at),
  KEY idx_archive_issue_date (log_date, resolved_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
