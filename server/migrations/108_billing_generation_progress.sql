ALTER TABLE billing_generation_ranges ADD COLUMN last_error TEXT NULL, ADD COLUMN last_attempt DATETIME(6) NULL;
