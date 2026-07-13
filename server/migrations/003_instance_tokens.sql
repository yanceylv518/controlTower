CREATE TABLE IF NOT EXISTS instance_tokens (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  instance_id VARCHAR(64) NOT NULL,
  token_hash CHAR(64) NOT NULL UNIQUE,
  created_at DATETIME(3) NOT NULL,
  expires_at DATETIME(3) NULL,
  INDEX idx_instance_tokens_instance (instance_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
