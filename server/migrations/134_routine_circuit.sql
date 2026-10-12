ALTER TABLE tuning_continuous_states
  ADD COLUMN routine_circuit_latched TINYINT(1) NOT NULL DEFAULT 0;
