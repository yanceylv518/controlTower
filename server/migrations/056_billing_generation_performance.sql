ALTER TABLE billing_compact_daily_totals
  ADD KEY idx_billing_compact_report_user (instance_id,bill_day,user_id,job_id),
  ADD KEY idx_billing_compact_report_channel (instance_id,bill_day,channel_id,job_id);

ALTER TABLE billing_jobs
  ADD COLUMN publish_attempts INT NOT NULL DEFAULT 0 AFTER completed_steps;
