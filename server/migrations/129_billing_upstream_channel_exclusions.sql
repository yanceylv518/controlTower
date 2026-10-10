CREATE TABLE IF NOT EXISTS billing_upstream_channel_exclusions (
 instance_id VARCHAR(64) NOT NULL,
 channel_id BIGINT NOT NULL,
 created_at DATETIME(6) NOT NULL,
 PRIMARY KEY(instance_id,channel_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
