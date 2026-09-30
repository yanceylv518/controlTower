CREATE TABLE IF NOT EXISTS billing_day_checks (
 instance_id VARCHAR(64) NOT NULL,
 kind VARCHAR(32) NOT NULL,
 subject_id BIGINT NOT NULL,
 bill_day DATE NOT NULL,
 has_consumption BOOLEAN NOT NULL,
 checked_at DATETIME(6) NOT NULL,
 PRIMARY KEY(instance_id,kind,subject_id,bill_day)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_month_daily_sources (
 month_job_id VARCHAR(40) NOT NULL,
 daily_job_id VARCHAR(40) NOT NULL,
 PRIMARY KEY(month_job_id,daily_job_id),
 CONSTRAINT fk_billing_month_source FOREIGN KEY(month_job_id) REFERENCES billing_jobs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
