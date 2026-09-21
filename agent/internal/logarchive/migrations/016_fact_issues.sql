CREATE TABLE IF NOT EXISTS archive_fact_issues (
  day_version_id BINARY(16) NOT NULL,
  source_log_id BIGINT NOT NULL,
  issue_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  evidence_hash BINARY(32) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (day_version_id, source_log_id, issue_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
