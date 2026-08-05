ALTER TABLE billing_prices
  ADD COLUMN cache_write_price DECIMAL(12,6) NOT NULL DEFAULT 0 AFTER cache_price;

ALTER TABLE billing_hourly
  ADD COLUMN cache_write_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_tokens,
  ADD COLUMN cache_write_5m_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_write_tokens,
  ADD COLUMN cache_write_1h_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_write_5m_tokens;
ALTER TABLE billing_daily
  ADD COLUMN cache_write_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_tokens,
  ADD COLUMN cache_write_5m_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_write_tokens,
  ADD COLUMN cache_write_1h_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_write_5m_tokens;
ALTER TABLE billing_daily_versions
  ADD COLUMN cache_write_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_tokens,
  ADD COLUMN cache_write_5m_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_write_tokens,
  ADD COLUMN cache_write_1h_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_write_5m_tokens;
ALTER TABLE billing_channel_hourly
  ADD COLUMN cache_write_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_tokens,
  ADD COLUMN cache_write_5m_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_write_tokens,
  ADD COLUMN cache_write_1h_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_write_5m_tokens;
ALTER TABLE billing_channel_daily_versions
  ADD COLUMN cache_write_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_tokens,
  ADD COLUMN cache_write_5m_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_write_tokens,
  ADD COLUMN cache_write_1h_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_write_5m_tokens;

ALTER TABLE billing_anomaly_orders
  ADD COLUMN cache_write_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_tokens,
  ADD COLUMN cache_write_5m_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_write_tokens,
  ADD COLUMN cache_write_1h_tokens BIGINT NOT NULL DEFAULT 0 AFTER cache_write_5m_tokens,
  ADD COLUMN cache_write_price DECIMAL(18,6) NOT NULL DEFAULT 0 AFTER cache_price,
  ADD COLUMN cache_write_amount DECIMAL(18,6) NOT NULL DEFAULT 0 AFTER cache_amount;
