ALTER TABLE billing_jobs
  ADD COLUMN exclude_zero_output TINYINT(1) NOT NULL DEFAULT 0 AFTER user_id;
