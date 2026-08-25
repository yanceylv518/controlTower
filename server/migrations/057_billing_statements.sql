CREATE TABLE IF NOT EXISTS billing_statement_jobs (
  job_id VARCHAR(40) NOT NULL,
  statement_type VARCHAR(24) NOT NULL,
  subject_id BIGINT NOT NULL,
  subject_name VARCHAR(128) NOT NULL DEFAULT '',
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (job_id),
  KEY idx_billing_statement_subject (statement_type, subject_id),
  CONSTRAINT fk_billing_statement_job FOREIGN KEY (job_id) REFERENCES billing_jobs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_statements (
  id VARCHAR(40) NOT NULL, job_id VARCHAR(40) NOT NULL, instance_id VARCHAR(64) NOT NULL,
  statement_type VARCHAR(24) NOT NULL, subject_id BIGINT NOT NULL, subject_name VARCHAR(128) NOT NULL DEFAULT '',
  range_from DATETIME NOT NULL, range_to DATETIME NOT NULL, normal_orders BIGINT NOT NULL DEFAULT 0,
  abnormal_orders BIGINT NOT NULL DEFAULT 0, archive_path VARCHAR(1000) NOT NULL DEFAULT '', created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id), UNIQUE KEY uq_billing_statement_job (job_id),
  KEY idx_billing_statement_list (instance_id, statement_type, created_at),
  CONSTRAINT fk_billing_statement_result_job FOREIGN KEY (job_id) REFERENCES billing_jobs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_statement_channels (
  job_id VARCHAR(40) NOT NULL, channel_id BIGINT NOT NULL, channel_name VARCHAR(128) NOT NULL DEFAULT '',
  PRIMARY KEY (job_id, channel_id),
  CONSTRAINT fk_billing_statement_channel_job FOREIGN KEY (job_id) REFERENCES billing_jobs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
