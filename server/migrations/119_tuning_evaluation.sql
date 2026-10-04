ALTER TABLE tuning_continuous_states ADD COLUMN evaluation_json MEDIUMTEXT NULL;

-- Older uncapped writes stored enqueue time as a successful write. Recover
-- only rows whose alleged last write is provably still unconfirmed/failed.
UPDATE tuning_continuous_states s
JOIN tuning_recommendations r ON r.instance_id=s.instance_id AND r.channel_id=s.channel_id
  AND r.acted_at=s.last_write_at AND r.proposed_weight=s.last_written_weight
JOIN channel_commands c ON c.id=r.command_id
JOIN channel_base_values b ON b.instance_id=s.instance_id AND b.channel_id=s.channel_id
SET s.last_written_weight=NULL,s.last_write_at=NULL,
  s.capacity_control_json=JSON_OBJECT('initialized',JSON_EXTRACT('true','$'),
    'fresh',JSON_EXTRACT(IF(b.max_rpm=0 AND b.max_tpm=0,'true','false'),'$'),
    'max_rpm',b.max_rpm,'max_tpm',b.max_tpm,'confirmed_weight',r.current_weight,
    'pending_command_id',c.id,'pending_weight',r.proposed_weight,'phase','awaiting_write')
WHERE c.status IN ('pending','delivered','failed','expired')
  AND r.rule IN ('weight_write','capacity_reduce','circuit_opened','circuit_recovered')
  AND COALESCE(JSON_EXTRACT(s.capacity_control_json,'$.initialized'),0)=0;
