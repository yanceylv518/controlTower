ALTER TABLE archive_datasets ADD COLUMN source_retained_from DATE NULL;
ALTER TABLE archive_datasets ADD COLUMN coverage_policy_json JSON NULL;
ALTER TABLE archive_datasets ADD COLUMN coverage_policy_revision BIGINT UNSIGNED NOT NULL DEFAULT 0;
ALTER TABLE archive_datasets ADD COLUMN coverage_confirmed_by VARCHAR(128) NULL;
ALTER TABLE archive_datasets ADD COLUMN coverage_confirmed_at DATETIME(6) NULL;

ALTER TABLE archive_tasks ADD COLUMN log_date DATE NULL;
ALTER TABLE archive_tasks ADD COLUMN next_attempt_at DATETIME(6) NULL;
ALTER TABLE archive_tasks ADD COLUMN lease_session VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL;
ALTER TABLE archive_tasks ADD COLUMN progress_json JSON NULL;
ALTER TABLE archive_tasks ADD COLUMN dispatched_at DATETIME(6) NULL;
ALTER TABLE archive_tasks ADD KEY idx_archive_backfill_date(dataset_id,log_date,created_at);
ALTER TABLE archive_tasks ADD KEY idx_archive_backfill_queue(dataset_id,status,next_attempt_at,created_at);
