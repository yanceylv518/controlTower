CREATE TABLE IF NOT EXISTS billing_month_coverage (
 job_id VARCHAR(40) NOT NULL PRIMARY KEY,
 coverage_json JSON NOT NULL,
 CONSTRAINT fk_billing_month_coverage FOREIGN KEY(job_id) REFERENCES billing_jobs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
