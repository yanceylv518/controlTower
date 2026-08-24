ALTER TABLE billing_anomaly_orders
  ADD COLUMN token_id BIGINT NOT NULL DEFAULT 0 AFTER username,
  ADD COLUMN token_name VARCHAR(128) NOT NULL DEFAULT '' AFTER token_id,
  ADD KEY idx_billing_anomaly_job_token (job_id, user_id, token_id, created_at);
