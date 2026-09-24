-- Preserve the existing read predicate, including verified human system/agent names.
ALTER TABLE operation_audits ADD COLUMN is_manual_audit TINYINT GENERATED ALWAYS AS (
  COALESCE(actor_type,'') <> 'system'
  AND COALESCE(trigger_type,'') <> 'automatic'
  AND COALESCE(operation_type,'') <> 'tuning.auto_execute'
  AND ((actor_type='human' AND actor_role IN ('admin','viewer') AND auth_method IN ('session','web_session'))
    OR (COALESCE(actor_id,'') NOT IN ('system','agent') AND COALESCE(actor_id,'') NOT LIKE 'system:%' AND COALESCE(actor_id,'') NOT LIKE 'agent:%'))
) STORED;
CREATE INDEX idx_operation_audits_manual_created ON operation_audits (is_manual_audit, created_at, id);
CREATE INDEX idx_operation_audits_request ON operation_audits (request_id);
