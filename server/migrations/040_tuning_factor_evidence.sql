ALTER TABLE tuning_continuous_states
  ADD COLUMN metric_ttft_p50 DOUBLE NOT NULL DEFAULT 0 AFTER baseline_ready,
  ADD COLUMN metric_ttft_p90 DOUBLE NOT NULL DEFAULT 0 AFTER metric_ttft_p50,
  ADD COLUMN metric_ttft_p95 DOUBLE NOT NULL DEFAULT 0 AFTER metric_ttft_p90,
  ADD COLUMN baseline_ttft_p50 DOUBLE NOT NULL DEFAULT 0 AFTER metric_ttft_p95,
  ADD COLUMN baseline_ttft_p90 DOUBLE NOT NULL DEFAULT 0 AFTER baseline_ttft_p50,
  ADD COLUMN baseline_ttft_p95 DOUBLE NOT NULL DEFAULT 0 AFTER baseline_ttft_p90,
  ADD COLUMN metric_cache DOUBLE NOT NULL DEFAULT 0 AFTER baseline_ttft_p95,
  ADD COLUMN baseline_cache DOUBLE NOT NULL DEFAULT 0 AFTER metric_cache,
  ADD COLUMN cache_ready BOOLEAN NOT NULL DEFAULT FALSE AFTER baseline_cache,
  ADD COLUMN metric_otps DOUBLE NOT NULL DEFAULT 0 AFTER cache_ready,
  ADD COLUMN baseline_otps DOUBLE NOT NULL DEFAULT 0 AFTER metric_otps,
  ADD COLUMN otps_ready BOOLEAN NOT NULL DEFAULT FALSE AFTER baseline_otps,
  ADD COLUMN smoothed_error_rate DOUBLE NOT NULL DEFAULT 0 AFTER otps_ready;
