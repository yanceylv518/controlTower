CREATE TABLE IF NOT EXISTS user_rate_seconds (
 instance_id VARCHAR(64) NOT NULL,
 user_id BIGINT NOT NULL,
 bucket_time DATETIME(6) NOT NULL,
 request_count BIGINT NOT NULL DEFAULT 0,
 tokens BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY (instance_id, user_id, bucket_time),
 KEY idx_user_rate_expiry (bucket_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS voice_alert_config (
 id INT PRIMARY KEY,
 config_json TEXT NOT NULL,
 updated_by VARCHAR(128) NOT NULL DEFAULT '',
 updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS voice_dispatch_guard (
 id INT PRIMARY KEY
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT IGNORE INTO voice_dispatch_guard(id) VALUES(1);

CREATE TABLE IF NOT EXISTS voice_alert_calls (
 id VARCHAR(15) PRIMARY KEY,
 site_id VARCHAR(64) NOT NULL,
 user_id BIGINT NOT NULL,
 phone VARCHAR(32) NOT NULL,
 window_end DATETIME(6) NOT NULL,
 min_tpm BIGINT NOT NULL,
 max_tpm BIGINT NOT NULL,
 direction VARCHAR(8) NOT NULL DEFAULT '',
 status VARCHAR(32) NOT NULL,
 call_id VARCHAR(128) NOT NULL DEFAULT '',
 request_id VARCHAR(128) NOT NULL DEFAULT '',
 result_code VARCHAR(128) NOT NULL DEFAULT '',
 created_at DATETIME(6) NOT NULL,
 suppress_until DATETIME(6) NOT NULL,
 KEY idx_voice_customer(site_id,user_id,phone,suppress_until),
 KEY idx_voice_phone(phone,created_at),
 KEY idx_voice_created(created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
