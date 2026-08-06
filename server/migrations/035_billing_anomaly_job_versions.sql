-- The sentinel column makes startup replays fail atomically with a tolerated
-- duplicate-column error (1060) before any work happens. Without it this
-- ALTER succeeds on every replay and rebuilds the whole table each server
-- start (ApplyDir re-runs all files -- same lesson as migration 010).
-- No semicolons in comments: the migration runner splits statements on them.
ALTER TABLE billing_anomaly_orders
  ADD COLUMN pk_job_scoped TINYINT(1) NOT NULL DEFAULT 1,
  DROP PRIMARY KEY,
  ADD PRIMARY KEY (job_id, instance_id, source_log_id);
