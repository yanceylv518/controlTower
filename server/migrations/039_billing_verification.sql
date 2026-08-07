CREATE TABLE IF NOT EXISTS billing_verification_jobs (
  job_id VARCHAR(40) NOT NULL,
  source_job_id VARCHAR(40) NOT NULL,
  PRIMARY KEY (job_id),
  KEY idx_billing_verification_source (source_job_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_verification_hourly (
  job_id VARCHAR(40) NOT NULL,
  bill_day DATE NOT NULL,
  user_id BIGINT NOT NULL,
  username VARCHAR(128) NOT NULL DEFAULT '',
  model_name VARCHAR(255) NOT NULL,
  group_name VARCHAR(64) NOT NULL DEFAULT '',
  source_rows BIGINT NOT NULL DEFAULT 0,
  normal_rows BIGINT NOT NULL DEFAULT 0,
  abnormal_rows BIGINT NOT NULL DEFAULT 0,
  source_quota BIGINT NOT NULL DEFAULT 0,
  normal_quota BIGINT NOT NULL DEFAULT 0,
  abnormal_quota BIGINT NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (job_id,bill_day,user_id,model_name,group_name),
  KEY idx_billing_verification_day (job_id,bill_day)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_verification_results (
  job_id VARCHAR(40) NOT NULL,
  source_job_id VARCHAR(40) NOT NULL,
  day DATE NOT NULL,
  user_id BIGINT NOT NULL,
  username VARCHAR(128) NOT NULL DEFAULT '',
  model_name VARCHAR(255) NOT NULL,
  group_name VARCHAR(64) NOT NULL DEFAULT '',
  source_rows BIGINT NOT NULL DEFAULT 0,
  verified_normal_rows BIGINT NOT NULL DEFAULT 0,
  billed_normal_rows BIGINT NOT NULL DEFAULT 0,
  verified_abnormal_rows BIGINT NOT NULL DEFAULT 0,
  billed_abnormal_rows BIGINT NOT NULL DEFAULT 0,
  source_quota BIGINT NOT NULL DEFAULT 0,
  verified_normal_quota BIGINT NOT NULL DEFAULT 0,
  billed_normal_quota BIGINT NOT NULL DEFAULT 0,
  verified_abnormal_quota BIGINT NOT NULL DEFAULT 0,
  billed_abnormal_quota BIGINT NOT NULL DEFAULT 0,
  status VARCHAR(24) NOT NULL DEFAULT 'mismatch',
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (job_id,day,user_id,model_name,group_name),
  KEY idx_billing_verification_result_status (job_id,status,day)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
