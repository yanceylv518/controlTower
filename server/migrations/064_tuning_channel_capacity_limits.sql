ALTER TABLE channel_base_values
  ADD COLUMN max_rpm BIGINT NOT NULL DEFAULT 0 AFTER base_priority,
  ADD COLUMN max_tpm BIGINT NOT NULL DEFAULT 0 AFTER max_rpm;

ALTER TABLE tuning_continuous_states
  ADD COLUMN metric_rpm DOUBLE NOT NULL DEFAULT 0 AFTER last_observed_errors,
  ADD COLUMN metric_tpm DOUBLE NOT NULL DEFAULT 0 AFTER metric_rpm,
  ADD COLUMN capacity_limited TINYINT(1) NOT NULL DEFAULT 0 AFTER metric_tpm;
