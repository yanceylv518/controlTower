ALTER TABLE billing_request_details
  ADD COLUMN matched_tier VARCHAR(128) NOT NULL DEFAULT '' AFTER billing_mode;
