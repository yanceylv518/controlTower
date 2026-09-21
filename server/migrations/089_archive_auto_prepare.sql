ALTER TABLE site_log_archive_control ADD COLUMN prepare_json JSON NULL;
ALTER TABLE log_archive_targets ADD COLUMN auto_prepare BOOLEAN NOT NULL DEFAULT FALSE;
