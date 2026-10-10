-- Frozen peak/off-peak statistics. Missing rows on old bills mean unavailable.
CREATE TABLE billing_tier_statistics (
    job_id VARCHAR(40) NOT NULL,
    bill_day DATE NOT NULL,
    user_id BIGINT NOT NULL,
    statistics_json MEDIUMTEXT NOT NULL,
    PRIMARY KEY (job_id, bill_day, user_id),
    CONSTRAINT fk_billing_tier_statistics_job FOREIGN KEY (job_id) REFERENCES billing_jobs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;