CREATE TABLE IF NOT EXISTS archive_read_connections (
 site_id VARCHAR(64) NOT NULL PRIMARY KEY,
 config_json JSON NOT NULL,
 encrypted_password TEXT NOT NULL,
 version BIGINT UNSIGNED NOT NULL,
 updated_by VARCHAR(128) NOT NULL,
 updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
