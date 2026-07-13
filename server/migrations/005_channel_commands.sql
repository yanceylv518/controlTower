CREATE TABLE IF NOT EXISTS channel_commands (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  instance_id VARCHAR(64) NOT NULL,
  channel_id BIGINT NOT NULL,
  command_type VARCHAR(32) NOT NULL,
  payload_json TEXT NOT NULL,
  status VARCHAR(16) NOT NULL,
  created_by VARCHAR(64) NOT NULL,
  error_summary VARCHAR(500) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  INDEX idx_channel_commands_instance (instance_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
