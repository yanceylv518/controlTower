-- Stable business ownership. instance_id is an executor only for new Agent commands.
-- Legacy rows remain unknown until the bounded evidence backfill examines them.
ALTER TABLE channel_commands
 ADD COLUMN site_id VARCHAR(64) NOT NULL DEFAULT '',
 ADD COLUMN execution_kind VARCHAR(24) NOT NULL DEFAULT 'unknown',
 ADD COLUMN ownership_evidence VARCHAR(32) NOT NULL DEFAULT '',
 ADD COLUMN legacy_instance_id VARCHAR(64) NOT NULL DEFAULT '',
 ADD INDEX idx_command_executor_history (instance_id,created_at,id),
 ADD INDEX idx_command_site_history (site_id,created_at,id),
 ADD INDEX idx_command_site_channel (site_id,channel_id,created_at,id),
 ADD INDEX idx_command_site_retention (site_id,status,created_at,id),
 ADD INDEX idx_command_owner_backfill (ownership_evidence,id);
ALTER TABLE operation_audits
 ADD COLUMN site_id VARCHAR(64) NOT NULL DEFAULT '',
 ADD COLUMN ownership_evidence VARCHAR(32) NOT NULL DEFAULT '',
 ADD INDEX idx_audit_site_channel (site_id,target_type,target_id,created_at,id),
 ADD INDEX idx_audit_site_retention (site_id,target_type,created_at,id),
 ADD INDEX idx_audit_owner_backfill (ownership_evidence,target_type,id);
