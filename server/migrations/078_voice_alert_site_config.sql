CREATE TABLE IF NOT EXISTS voice_alert_site_config (
 site_id VARCHAR(64) NOT NULL PRIMARY KEY,
 config_json TEXT NOT NULL,
 updated_by VARCHAR(128) NOT NULL DEFAULT '',
 updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
