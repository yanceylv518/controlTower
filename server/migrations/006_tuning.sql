CREATE TABLE IF NOT EXISTS tuning_policies (
  instance_id VARCHAR(64) NOT NULL PRIMARY KEY,
  policy_json TEXT NOT NULL,
  mode VARCHAR(16) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(128) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tuning_recommendations (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  instance_id VARCHAR(64) NOT NULL,
  channel_id BIGINT NOT NULL,
  channel_name VARCHAR(255) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  rule VARCHAR(16) NOT NULL,
  evidence_json TEXT NOT NULL,
  current_weight BIGINT NOT NULL,
  proposed_weight BIGINT NOT NULL,
  current_priority BIGINT NULL,
  proposed_priority BIGINT NULL,
  mode_at_creation VARCHAR(16) NOT NULL,
  status VARCHAR(16) NOT NULL,
  command_id VARCHAR(64) NULL,
  outcome_json TEXT NULL,
  outcome_at DATETIME(6) NULL,
  hit TINYINT NULL,
  INDEX idx_tuning_instance_created (instance_id, created_at),
  INDEX idx_tuning_channel_created (instance_id, channel_id, created_at),
  INDEX idx_tuning_outcome (outcome_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
