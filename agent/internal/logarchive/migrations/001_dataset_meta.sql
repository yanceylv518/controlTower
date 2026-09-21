CREATE TABLE IF NOT EXISTS archive_dataset_meta (
  singleton_id TINYINT UNSIGNED NOT NULL,
  dataset_id BINARY(16) NOT NULL,
  source_generation_id BINARY(16) NOT NULL,
  site_id VARCHAR(64) NOT NULL,
  format_version INT UNSIGNED NOT NULL,
  source_identity_hash BINARY(32) NOT NULL,
  schema_fingerprint BINARY(32) NOT NULL,
  writer_epoch BIGINT UNSIGNED NOT NULL DEFAULT 0,
  writer_session BINARY(16) NULL,
  writer_lease_until DATETIME(6) NULL,
  catalog_revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
  unscoped_blocking_issues BIGINT UNSIGNED NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (singleton_id),
  UNIQUE KEY uq_archive_dataset_id (dataset_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
