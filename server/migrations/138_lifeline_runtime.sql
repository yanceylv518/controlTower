CREATE TABLE IF NOT EXISTS tuning_probe_runtime (
  instance_id VARCHAR(64) NOT NULL,
  channel_id BIGINT NOT NULL,
  confirmed_at DATETIME(6) NOT NULL,
  snapshot_json JSON NOT NULL,
  PRIMARY KEY(instance_id,channel_id),
  KEY idx_probe_runtime_time(confirmed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE tuning_probe_records
  ADD COLUMN round_id VARCHAR(64) NOT NULL DEFAULT '',
  ADD COLUMN round_index INT NOT NULL DEFAULT 0,
  ADD KEY idx_probe_round(instance_id,round_id,channel_id,started_at,id);
