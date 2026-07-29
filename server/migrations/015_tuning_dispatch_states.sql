CREATE TABLE IF NOT EXISTS tuning_dispatch_states (
  instance_id VARCHAR(64) NOT NULL,
  channel_id BIGINT NOT NULL,
  model_name VARCHAR(255) NOT NULL,
  original_priority BIGINT NOT NULL,
  demoted_at DATETIME(6) NOT NULL,
  trial_attempts INT NOT NULL DEFAULT 0,
  next_trial_at DATETIME(6) NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (instance_id, channel_id),
  KEY idx_tuning_dispatch_trial (instance_id, next_trial_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
