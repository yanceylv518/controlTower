CREATE TABLE IF NOT EXISTS billing_model_metadata (
  instance_id VARCHAR(64) NOT NULL,
  model_name VARCHAR(255) NOT NULL,
  max_context_tokens BIGINT NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(128) NOT NULL,
  PRIMARY KEY (instance_id, model_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
