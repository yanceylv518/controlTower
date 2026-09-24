ALTER TABLE tuning_continuous_states
  ADD COLUMN circuit_disabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE tuning_continuous_states
  ADD COLUMN circuit_status_target INT NOT NULL DEFAULT 0;
ALTER TABLE tuning_continuous_states
  ADD COLUMN circuit_status_command_id VARCHAR(128) NOT NULL DEFAULT '';
