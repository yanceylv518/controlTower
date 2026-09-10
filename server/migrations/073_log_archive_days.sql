CREATE TABLE IF NOT EXISTS site_log_archive_days (
 site_id VARCHAR(64) NOT NULL,
 log_date VARCHAR(10) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 archived_rows DECIMAL(38,0) NOT NULL,
 request_rows DECIMAL(38,0) NOT NULL,
 error_rows DECIMAL(38,0) NOT NULL,
 last_log_id BIGINT NOT NULL,
 verified_at DATETIME(6) NOT NULL,
 PRIMARY KEY(site_id,log_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
