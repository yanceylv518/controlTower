ALTER TABLE billing_jobs
  ADD COLUMN request_key VARCHAR(191) NULL AFTER id,
  ADD UNIQUE KEY uk_billing_jobs_request_key (request_key);
