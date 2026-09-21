CREATE TABLE IF NOT EXISTS archive_billing_evidence_template (
  evidence_hash BINARY(32) NOT NULL,
  codec_version INT UNSIGNED NOT NULL,
  source_schema_hash BINARY(32) NOT NULL,
  payload LONGBLOB NOT NULL,
  payload_bytes BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (evidence_hash)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
