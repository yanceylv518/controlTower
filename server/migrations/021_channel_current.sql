CREATE TABLE IF NOT EXISTS channel_current (
  instance_id VARCHAR(64) NOT NULL,
  channel_id BIGINT NOT NULL,
  id VARCHAR(64) NOT NULL,
  channel_name VARCHAR(255) NOT NULL,
  status VARCHAR(64) NOT NULL,
  weight BIGINT NOT NULL,
  models_text TEXT NOT NULL,
  group_name VARCHAR(128) NULL,
  priority BIGINT NULL,
  captured_at DATETIME(6) NOT NULL,
  PRIMARY KEY (instance_id, channel_id),
  KEY idx_channel_current_captured (captured_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO channel_current (
  instance_id, channel_id, id, channel_name, status, weight,
  models_text, group_name, priority, captured_at
)
SELECT
  snapshots.instance_id,
  snapshots.channel_id,
  snapshots.id,
  snapshots.channel_name,
  snapshots.status,
  snapshots.weight,
  snapshots.models_text,
  snapshots.group_name,
  snapshots.priority,
  snapshots.captured_at
FROM channel_snapshots snapshots
JOIN (
  SELECT instance_id, channel_id, MAX(captured_at) AS captured_at
  FROM channel_snapshots
  GROUP BY instance_id, channel_id
) latest
  ON latest.instance_id = snapshots.instance_id
 AND latest.channel_id = snapshots.channel_id
 AND latest.captured_at = snapshots.captured_at
ON DUPLICATE KEY UPDATE
  id = IF(VALUES(captured_at) >= channel_current.captured_at, VALUES(id), channel_current.id),
  channel_name = IF(VALUES(captured_at) >= channel_current.captured_at, VALUES(channel_name), channel_current.channel_name),
  status = IF(VALUES(captured_at) >= channel_current.captured_at, VALUES(status), channel_current.status),
  weight = IF(VALUES(captured_at) >= channel_current.captured_at, VALUES(weight), channel_current.weight),
  models_text = IF(VALUES(captured_at) >= channel_current.captured_at, VALUES(models_text), channel_current.models_text),
  group_name = IF(VALUES(captured_at) >= channel_current.captured_at, VALUES(group_name), channel_current.group_name),
  priority = IF(VALUES(captured_at) >= channel_current.captured_at, VALUES(priority), channel_current.priority),
  captured_at = GREATEST(channel_current.captured_at, VALUES(captured_at));
