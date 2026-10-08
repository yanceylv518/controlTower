ALTER TABLE billing_generation_ranges ADD COLUMN exclude_zero_output BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE billing_generation_tasks ADD COLUMN exclude_zero_output BOOLEAN NOT NULL DEFAULT FALSE;
