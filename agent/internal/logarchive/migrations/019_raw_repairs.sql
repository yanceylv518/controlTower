CREATE TABLE IF NOT EXISTS archive_raw_repairs (
  batch_id BINARY(16) NOT NULL,
  source_id BIGINT NOT NULL,
  target_table VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  task_id BINARY(16) NOT NULL,
  attempt INT UNSIGNED NOT NULL,
  writer_epoch BIGINT UNSIGNED NOT NULL,
  repair_kind VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  before_date DATE NULL,
  after_date DATE NOT NULL,
  before_row_hash BINARY(32) NULL,
  after_row_hash BINARY(32) NOT NULL,
  committed_at DATETIME(6) NOT NULL,
  PRIMARY KEY (batch_id, source_id),
  KEY idx_archive_raw_repair_source (source_id, committed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
