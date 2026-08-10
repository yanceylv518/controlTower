ALTER TABLE tuning_continuous_states
  ADD COLUMN write_failure_streak INT NOT NULL DEFAULT 0 AFTER soft_start_pending,
  ADD COLUMN last_write_failure_at DATETIME NULL AFTER write_failure_streak,
  ADD COLUMN last_write_error VARCHAR(512) NOT NULL DEFAULT '' AFTER last_write_failure_at;
