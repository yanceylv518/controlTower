ALTER TABLE metric_1m
  ADD COLUMN otps_output_tokens BIGINT NOT NULL DEFAULT 0 AFTER ttft_p95_ms,
  ADD COLUMN otps_duration_seconds DOUBLE NOT NULL DEFAULT 0 AFTER otps_output_tokens;

ALTER TABLE metric_5m
  ADD COLUMN otps_output_tokens BIGINT NOT NULL DEFAULT 0 AFTER ttft_p95_ms,
  ADD COLUMN otps_duration_seconds DOUBLE NOT NULL DEFAULT 0 AFTER otps_output_tokens;
