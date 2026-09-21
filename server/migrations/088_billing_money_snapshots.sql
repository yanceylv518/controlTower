CREATE TABLE IF NOT EXISTS billing_money_snapshots (
  id CHAR(64) NOT NULL,
  instance_id VARCHAR(64) NOT NULL,
  observed_at DATETIME(6) NOT NULL,
  snapshot_json LONGTEXT NOT NULL,
  PRIMARY KEY (id),
  KEY idx_money_site_observed (instance_id, observed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_job_money_snapshots (
  job_id VARCHAR(40) NOT NULL,
  snapshot_id CHAR(64) NOT NULL,
  PRIMARY KEY (job_id),
  CONSTRAINT fk_money_job FOREIGN KEY (job_id) REFERENCES billing_jobs(id) ON DELETE CASCADE,
  CONSTRAINT fk_money_snapshot FOREIGN KEY (snapshot_id) REFERENCES billing_money_snapshots(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
