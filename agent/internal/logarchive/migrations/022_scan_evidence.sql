CREATE TABLE IF NOT EXISTS archive_scan_evidence (
  task_id BINARY(16) NOT NULL,
  attempt INT UNSIGNED NOT NULL,
  run_id BINARY(16) NOT NULL,
  scan_json JSON NOT NULL,
  last_batch_id BINARY(16) NOT NULL,
  completed TINYINT UNSIGNED NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (task_id, attempt),
  UNIQUE KEY uq_archive_scan_evidence_run (run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
