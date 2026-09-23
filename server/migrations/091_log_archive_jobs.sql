CREATE TABLE IF NOT EXISTS log_archive_control (
 site_id VARCHAR(64) PRIMARY KEY, config_json JSON NOT NULL, status_json JSON NOT NULL,
 session_id VARCHAR(32) NOT NULL DEFAULT '', lease_until DATETIME(6) NULL, seen_at DATETIME(6) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE IF NOT EXISTS log_archive_executors (
 instance_id VARCHAR(64) NOT NULL, agent_id VARCHAR(64) NOT NULL, configured BOOLEAN NOT NULL,
 seen_at DATETIME(6) NOT NULL, PRIMARY KEY(instance_id,agent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE IF NOT EXISTS log_archive_day_reports (
 site_id VARCHAR(64) NOT NULL, log_date DATE NOT NULL, detail_json JSON NOT NULL,
 updated_at DATETIME(6) NOT NULL, PRIMARY KEY(site_id,log_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
