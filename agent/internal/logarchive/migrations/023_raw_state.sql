CREATE TABLE IF NOT EXISTS archive_raw_state (
  id BIGINT NOT NULL,
  contribution JSON NOT NULL,
  raw_row_hash BINARY(32) NULL,
  last_batch_id BINARY(16) NULL,
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
