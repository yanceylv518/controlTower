-- Drive event import from circuit decisions, not every ordinary weight change.
CREATE INDEX idx_tuning_circuit_import ON tuning_recommendations (rule,mode_at_creation,status,created_at,id);
