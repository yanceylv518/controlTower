ALTER TABLE billing_compact_daily_totals
 ADD COLUMN before_known_count BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN settlement_discount VARCHAR(32) NOT NULL DEFAULT '';
