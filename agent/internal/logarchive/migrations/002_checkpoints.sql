CREATE TABLE IF NOT EXISTS archive_checkpoints (
  stream_key VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  stream_type VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  task_id BINARY(16) NULL,
  from_unix BIGINT NULL,
  to_unix BIGINT NULL,
  after_created_unix BIGINT NULL,
  after_id BIGINT NOT NULL,
  cursor_version INT UNSIGNED NOT NULL,
  last_batch_id BINARY(16) NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (stream_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
