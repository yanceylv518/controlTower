ALTER TABLE tuning_continuous_states
  ADD COLUMN last_probe_command_id VARCHAR(64) NULL AFTER probe_command_id;
