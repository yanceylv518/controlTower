CREATE TABLE IF NOT EXISTS channel_group_presets (
  site_id VARCHAR(191) NOT NULL PRIMARY KEY,
  items_json LONGTEXT NOT NULL,
  revision BIGINT NOT NULL DEFAULT 1,
  updated_by VARCHAR(191) NOT NULL,
  updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
