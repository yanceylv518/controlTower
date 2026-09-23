-- Independent two-task control. Migration 071 already owns log_archive_control
-- with an instance_id key. Leave that legacy table and its data untouched.
CREATE TABLE IF NOT EXISTS log_archive_job_control (
 site_id VARCHAR(64) NOT NULL PRIMARY KEY,
 config_json JSON NOT NULL,
 status_json JSON NOT NULL,
 session_id VARCHAR(32) NOT NULL DEFAULT '',
 lease_until DATETIME(6) NULL,
 seen_at DATETIME(6) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
