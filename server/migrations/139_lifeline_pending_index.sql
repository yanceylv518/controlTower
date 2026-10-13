-- Current state reads only in-flight probes without scanning retained history.
ALTER TABLE tuning_probe_records
  ADD KEY idx_probe_pending(instance_id,outcome,deadline_at,round_id,channel_id);
