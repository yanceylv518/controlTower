CREATE TABLE IF NOT EXISTS system_settings (
  setting_key VARCHAR(128) NOT NULL,
  setting_value VARCHAR(255) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(128) NOT NULL,
  PRIMARY KEY (setting_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
