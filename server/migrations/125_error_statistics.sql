CREATE TABLE IF NOT EXISTS error_statistics_state (
 instance_id VARCHAR(128) NOT NULL PRIMARY KEY,
 started_at DATETIME(6) NOT NULL,
 observed_at DATETIME(6) NOT NULL,
 dropped BIGINT NOT NULL DEFAULT 0
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE IF NOT EXISTS error_statistics_batches (
 instance_id VARCHAR(128) NOT NULL,
 batch_id VARCHAR(128) NOT NULL,
 created_at DATETIME(6) NOT NULL,
 PRIMARY KEY(instance_id,batch_id), KEY idx_error_batch_time(created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE IF NOT EXISTS error_statistics_minutes (
 instance_id VARCHAR(128) NOT NULL,
 bucket_time DATETIME NOT NULL,
 user_id BIGINT NOT NULL,
 channel_id BIGINT NOT NULL,
 model_hash BINARY(32) NOT NULL,
 model_name VARCHAR(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 error_code VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 record_count BIGINT NOT NULL,
 PRIMARY KEY(instance_id,bucket_time,user_id,channel_id,model_hash,error_code),
 KEY idx_error_user(instance_id,user_id,bucket_time),
 KEY idx_error_channel(instance_id,channel_id,bucket_time),
 KEY idx_error_model(instance_id,model_hash,bucket_time),
 KEY idx_error_time(bucket_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE error_statistics_state ADD COLUMN covered_until DATETIME(6) NULL;
ALTER TABLE error_statistics_state ADD COLUMN last_loss_at DATETIME(6) NULL;
