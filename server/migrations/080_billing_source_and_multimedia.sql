-- Existing tasks retain their original pricing and usage semantics, including
-- pending tasks and staged detail files. New statement requests opt into v1.
ALTER TABLE billing_jobs
  ADD COLUMN pricing_source VARCHAR(24) NOT NULL DEFAULT 'recalculate',
  ADD COLUMN usage_version INT NOT NULL DEFAULT 0;

-- Do not infer historical multimedia usage or regenerate completed bills.
ALTER TABLE billing_compact_daily_totals
  ADD COLUMN image_input_tokens BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN image_output_tokens BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN audio_input_tokens BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN audio_output_tokens BIGINT NOT NULL DEFAULT 0;
