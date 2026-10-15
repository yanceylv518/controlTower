-- Narrow covering indexes for bounded channel-history reference checks.
ALTER TABLE tuning_recommendations ADD INDEX idx_recommendation_history_guard (command_id,created_at);
ALTER TABLE channel_control_requests ADD INDEX idx_request_history_guard (command_id,started_at);
ALTER TABLE tuning_probe_records ADD INDEX idx_probe_history_guard (instance_id,round_id,outcome,deadline_at);
ALTER TABLE operation_audits
 ADD INDEX idx_audit_command_guard (id,created_at,updated_at),
 ADD INDEX idx_audit_correlation_guard (correlation_id,created_at,updated_at);
