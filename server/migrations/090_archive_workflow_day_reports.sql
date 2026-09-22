CREATE TABLE IF NOT EXISTS archive_workflow_day_reports (
  dataset_id BINARY(16) NOT NULL,
  workflow_id BINARY(16) NOT NULL,
  log_date DATE NOT NULL,
  report_json JSON NOT NULL,
  observed_at DATETIME(6) NOT NULL,
  PRIMARY KEY (dataset_id, workflow_id, log_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
