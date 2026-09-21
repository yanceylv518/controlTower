CREATE TABLE IF NOT EXISTS archive_datasets (
 dataset_id BINARY(16) NOT NULL,
 site_id VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 source_generation_id BINARY(16) NOT NULL,
 storage_ref VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 source_identity_json JSON NOT NULL,
 archive_format_version INT NOT NULL,
 lifecycle_state VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 coverage_from DATE NULL,
 coverage_evidence_json JSON NULL,
 config_revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
 observed_catalog_revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
 observed_catalog_hash BINARY(32) NULL,
 unscoped_blocking_issues BIGINT UNSIGNED NOT NULL DEFAULT 0,
 current_config_version_id BINARY(16) NULL,
 created_at DATETIME(6) NOT NULL,
 updated_at DATETIME(6) NOT NULL,
 PRIMARY KEY(dataset_id),
 UNIQUE KEY uq_archive_dataset_generation(site_id,source_generation_id),
 UNIQUE KEY uq_archive_dataset_storage(storage_ref),
 KEY idx_archive_dataset_site(site_id,lifecycle_state)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS archive_day_catalog (
 dataset_id BINARY(16) NOT NULL,
 log_date DATE NOT NULL,
 state VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 current_version_id BINARY(16) NULL,
 manifest_hash BINARY(32) NULL,
 catalog_revision BIGINT UNSIGNED NOT NULL,
 mutation_revision BIGINT UNSIGNED NOT NULL,
 all_rows DECIMAL(38,0) NULL,
 consume_rows DECIMAL(38,0) NULL,
 consume_quota DECIMAL(38,0) NULL,
 verified_at DATETIME(6) NULL,
 block_reason VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
 synced_at DATETIME(6) NOT NULL,
 PRIMARY KEY(dataset_id,log_date),
 KEY idx_archive_catalog_state(dataset_id,state,log_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS archive_tasks (
 task_id BINARY(16) NOT NULL,
 dataset_id BINARY(16) NOT NULL,
 source_generation_id BINARY(16) NOT NULL,
 task_type VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 request_key BINARY(32) NOT NULL,
 request_hash BINARY(32) NOT NULL,
 from_unix BIGINT NOT NULL,
 to_unix BIGINT NOT NULL,
 status VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 parameters_json JSON NOT NULL,
 attempt_no INT NOT NULL DEFAULT 0,
 lease_epoch BIGINT UNSIGNED NOT NULL DEFAULT 0,
 result_run_id BINARY(16) NULL,
 error_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
 requested_by VARCHAR(128) NOT NULL,
 created_at DATETIME(6) NOT NULL,
 updated_at DATETIME(6) NOT NULL,
 finished_at DATETIME(6) NULL,
 PRIMARY KEY(task_id),
 UNIQUE KEY uq_archive_task_request(dataset_id,request_key),
 KEY idx_archive_task_status(dataset_id,status,created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE site_log_archive_control ADD COLUMN active_dataset_id BINARY(16) NULL;
ALTER TABLE site_log_archive_control ADD COLUMN required_protocol_version INT NOT NULL DEFAULT 1;
ALTER TABLE log_archive_targets ADD COLUMN protocol_version INT NOT NULL DEFAULT 1;
ALTER TABLE log_archive_targets ADD COLUMN foundation_json JSON NULL;
