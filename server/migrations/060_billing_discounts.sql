CREATE TABLE IF NOT EXISTS billing_discount_rules (
  id BIGINT NOT NULL AUTO_INCREMENT,
  instance_id VARCHAR(64) NOT NULL,
  discount_type VARCHAR(32) NOT NULL,
  subject_id BIGINT NOT NULL,
  channel_id BIGINT NOT NULL DEFAULT 0,
  model_name VARCHAR(255) NOT NULL DEFAULT '',
  discount DECIMAL(12,6) NOT NULL,
  effective_from DATE NOT NULL,
  effective_to DATE NULL,
  remark VARCHAR(500) NOT NULL DEFAULT '',
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(128) NOT NULL DEFAULT '',
  PRIMARY KEY (id),
  KEY idx_billing_discount_match (instance_id,discount_type,subject_id,channel_id,model_name,effective_from,effective_to)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_statement_discount_snapshots (
  id BIGINT NOT NULL AUTO_INCREMENT,
  job_id VARCHAR(40) NOT NULL,
  discount_type VARCHAR(32) NOT NULL,
  subject_id BIGINT NOT NULL,
  channel_id BIGINT NOT NULL DEFAULT 0,
  channel_name VARCHAR(255) NOT NULL DEFAULT '',
  model_name VARCHAR(255) NOT NULL DEFAULT '',
  discount DECIMAL(12,6) NOT NULL,
  effective_from DATE NOT NULL,
  effective_to DATE NULL,
  source_rule_id BIGINT NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_statement_discount_match (job_id,channel_id,model_name,effective_from,effective_to),
  CONSTRAINT fk_statement_discount_job FOREIGN KEY (job_id) REFERENCES billing_jobs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
