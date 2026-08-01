CREATE TABLE IF NOT EXISTS channel_base_values (
  instance_id VARCHAR(64) NOT NULL,
  channel_id BIGINT NOT NULL,
  model_name VARCHAR(255) NOT NULL,
  base_weight BIGINT NOT NULL,
  base_priority BIGINT NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(128) NOT NULL,
  PRIMARY KEY (instance_id, channel_id),
  KEY idx_channel_base_values_instance_model (instance_id, model_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
