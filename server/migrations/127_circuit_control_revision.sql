ALTER TABLE tuning_continuous_states
  ADD COLUMN control_revision BIGINT NOT NULL DEFAULT 0;
