ALTER TABLE tuning_continuous_states
  ADD COLUMN phase VARCHAR(32) NOT NULL DEFAULT 'normal' AFTER paused_reason,
  ADD COLUMN circuit_opened_at DATETIME(6) NULL AFTER phase,
  ADD COLUMN next_probe_at DATETIME(6) NULL AFTER circuit_opened_at,
  ADD COLUMN probe_command_id VARCHAR(64) NULL AFTER next_probe_at,
  ADD COLUMN probe_attempts INT NOT NULL DEFAULT 0 AFTER probe_command_id,
  ADD COLUMN probe_successes INT NOT NULL DEFAULT 0 AFTER probe_attempts,
  ADD COLUMN probe_duration_sum DOUBLE NOT NULL DEFAULT 0 AFTER probe_successes,
  ADD COLUMN original_priority BIGINT NULL AFTER probe_duration_sum,
  ADD COLUMN soft_start_pending TINYINT(1) NOT NULL DEFAULT 0 AFTER original_priority,
  ADD KEY idx_tuning_continuous_next_probe (instance_id, phase, next_probe_at);
