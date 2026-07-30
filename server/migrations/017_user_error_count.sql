ALTER TABLE metric_1m
  ADD COLUMN user_error_count BIGINT NOT NULL DEFAULT 0 AFTER error_count;

ALTER TABLE metric_5m
  ADD COLUMN user_error_count BIGINT NOT NULL DEFAULT 0 AFTER error_count;
