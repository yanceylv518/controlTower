ALTER TABLE tuning_continuous_states
  ADD COLUMN metric_ready BOOLEAN NOT NULL DEFAULT FALSE AFTER last_observed_errors,
  ADD COLUMN baseline_ready BOOLEAN NOT NULL DEFAULT FALSE AFTER metric_ready;
