CREATE TABLE IF NOT EXISTS log_archive_meta (
 singleton_id TINYINT PRIMARY KEY, schema_version INT NOT NULL, source_hash CHAR(64) NOT NULL,
 state_json JSON NOT NULL, updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS log_archive_days (
 log_date DATE PRIMARY KEY, revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
 state VARCHAR(16) NOT NULL DEFAULT 'pending', version_id CHAR(32) NOT NULL DEFAULT '',
 raw_rows BIGINT UNSIGNED NULL, step VARCHAR(32) NOT NULL DEFAULT '',
 error_code VARCHAR(256) NOT NULL DEFAULT '', updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS log_archive_batches (
 batch_id CHAR(32) PRIMARY KEY, task VARCHAR(16) NOT NULL, before_id BIGINT NOT NULL,
 after_id BIGINT NOT NULL, read_rows INT NOT NULL, inserted_rows INT NOT NULL,
 changed_rows INT NOT NULL, unchanged_rows INT NOT NULL, committed_at DATETIME(6) NOT NULL
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS log_archive_day_versions (
 version_id CHAR(32) PRIMARY KEY, log_date DATE NOT NULL, revision BIGINT UNSIGNED NOT NULL,
 row_count BIGINT UNSIGNED NOT NULL, content_hash CHAR(64) NOT NULL,
 parser_version INT NOT NULL, sealed_at DATETIME(6) NOT NULL,
 KEY(log_date,revision)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS log_archive_daily_stats (
 version_id CHAR(32) NOT NULL, group_hash CHAR(64) NOT NULL, log_date DATE NOT NULL,
 dimensions JSON NOT NULL, amounts JSON NOT NULL, PRIMARY KEY(version_id,group_hash), KEY(log_date)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS log_archive_issues (
 issue_id CHAR(32) PRIMARY KEY, log_date DATE NULL, step VARCHAR(32) NOT NULL,
 error_code VARCHAR(256) NOT NULL, created_at DATETIME(6) NOT NULL, KEY(log_date)
) ENGINE=InnoDB;
