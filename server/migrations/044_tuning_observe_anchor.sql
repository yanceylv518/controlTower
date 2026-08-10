ALTER TABLE tuning_continuous_states
  ADD COLUMN last_observed_weight BIGINT NULL AFTER last_write_error;
