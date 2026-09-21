CREATE TABLE IF NOT EXISTS archive_batch_receipts (
  batch_id BINARY(16) NOT NULL,
  stream_key VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  payload_hash BINARY(32) NOT NULL,
  writer_epoch BIGINT UNSIGNED NOT NULL,
  cursor_before_json JSON NOT NULL,
  cursor_after_json JSON NOT NULL,
  row_count BIGINT UNSIGNED NOT NULL,
  byte_count BIGINT UNSIGNED NOT NULL,
  affected_dates_json JSON NOT NULL,
  committed_at DATETIME(6) NOT NULL,
  PRIMARY KEY (batch_id),
  KEY idx_archive_receipt_stream (stream_key, committed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
