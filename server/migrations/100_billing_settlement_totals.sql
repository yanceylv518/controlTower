ALTER TABLE billing_compact_daily_totals ADD COLUMN empty_output_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE billing_compact_daily_totals ADD COLUMN empty_output_amount DECIMAL(30,12) NOT NULL DEFAULT 0;
ALTER TABLE billing_compact_daily_totals ADD COLUMN before_amount DECIMAL(30,12) NOT NULL DEFAULT 0;
