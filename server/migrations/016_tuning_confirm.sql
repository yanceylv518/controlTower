ALTER TABLE tuning_recommendations
  ADD COLUMN acted_by VARCHAR(128) NOT NULL DEFAULT '' AFTER command_id,
  ADD COLUMN acted_at DATETIME(6) NULL AFTER acted_by,
  ADD INDEX idx_tuning_status_created (status, created_at);
