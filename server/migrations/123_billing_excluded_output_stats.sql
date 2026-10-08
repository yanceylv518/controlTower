CREATE TABLE billing_excluded_output_stats (
  job_id VARCHAR(40) NOT NULL,
  model_name VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  request_count BIGINT NOT NULL DEFAULT 0,
  total_amount DECIMAL(36,12) NOT NULL DEFAULT 0,
  PRIMARY KEY (job_id, model_name),
  CONSTRAINT fk_billing_excluded_output_job FOREIGN KEY (job_id) REFERENCES billing_jobs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
