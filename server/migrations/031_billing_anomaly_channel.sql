ALTER TABLE billing_anomaly_orders
  ADD COLUMN channel_id BIGINT NOT NULL DEFAULT 0 AFTER username,
  ADD COLUMN channel_name VARCHAR(128) NOT NULL DEFAULT '' AFTER channel_id,
  ADD KEY idx_billing_anomaly_channel_time (instance_id, channel_id, created_at, source_log_id);
