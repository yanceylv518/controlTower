ALTER TABLE billing_anomaly_orders
  ADD COLUMN upstream_request_id VARCHAR(128) NOT NULL DEFAULT '' AFTER request_id,
  ADD COLUMN input_price DECIMAL(18,6) NOT NULL DEFAULT 0 AFTER max_context_tokens,
  ADD COLUMN output_price DECIMAL(18,6) NOT NULL DEFAULT 0 AFTER input_price,
  ADD COLUMN cache_price DECIMAL(18,6) NOT NULL DEFAULT 0 AFTER output_price,
  ADD COLUMN input_amount DECIMAL(18,6) NOT NULL DEFAULT 0 AFTER cache_price,
  ADD COLUMN output_amount DECIMAL(18,6) NOT NULL DEFAULT 0 AFTER input_amount,
  ADD COLUMN cache_amount DECIMAL(18,6) NOT NULL DEFAULT 0 AFTER output_amount,
  ADD COLUMN reference_amount DECIMAL(18,6) NOT NULL DEFAULT 0 AFTER cache_amount;
