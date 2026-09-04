CREATE TABLE IF NOT EXISTS channel_rate_seconds (
 instance_id VARCHAR(64) NOT NULL,
 channel_id BIGINT NOT NULL,
 bucket_time DATETIME(6) NOT NULL,
 request_count BIGINT NOT NULL DEFAULT 0,
 tokens BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (instance_id, bucket_time, channel_id),
 KEY idx_channel_rate_expiry (bucket_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
