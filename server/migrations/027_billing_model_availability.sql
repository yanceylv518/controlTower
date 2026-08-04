ALTER TABLE billing_model_metadata
  ADD COLUMN available TINYINT(1) NOT NULL DEFAULT 1 AFTER max_context_tokens;
