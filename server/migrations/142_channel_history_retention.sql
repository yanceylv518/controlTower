-- Channel history reads and bounded cleanup use separate access paths.
CREATE INDEX idx_audit_channel_history ON operation_audits (instance_id,target_type,target_id,created_at,id);
CREATE INDEX idx_audit_channel_retention ON operation_audits (instance_id,target_type,created_at,id);
CREATE INDEX idx_command_history_retention ON channel_commands (instance_id,status,created_at,id);
