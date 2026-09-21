CREATE TABLE IF NOT EXISTS archive_reconcile_issues (
  run_id BINARY(16) NOT NULL,
  source_id BIGINT NOT NULL,
  source_created_unix BIGINT NULL,
  target_created_unix BIGINT NULL,
  source_row_hash BINARY(32) NULL,
  target_row_hash BINARY(32) NULL,
  issue_kind VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (run_id, source_id),
  KEY idx_archive_reconcile_issue (run_id, issue_kind, source_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
