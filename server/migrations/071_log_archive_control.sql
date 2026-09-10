CREATE TABLE IF NOT EXISTS log_archive_control (
 instance_id VARCHAR(64) NOT NULL PRIMARY KEY,
 config_json JSON NOT NULL,
 status_json JSON NOT NULL,
 seen_at DATETIME(6) NULL,
 session_id VARCHAR(32) NOT NULL DEFAULT '',
 lease_until DATETIME(6) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
