CREATE TABLE IF NOT EXISTS operations_people (
 id VARCHAR(32) PRIMARY KEY,
 name VARCHAR(80) NOT NULL,
 phone VARCHAR(32) NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 revision BIGINT NOT NULL DEFAULT 1,
 updated_at DATETIME(6) NOT NULL,
 UNIQUE KEY uq_operations_phone(phone)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS trial_site_state (
 site_id VARCHAR(64) PRIMARY KEY,
 display_name VARCHAR(80) NOT NULL DEFAULT '',
 cursor_id BIGINT NOT NULL DEFAULT 0,
 initialized BOOLEAN NOT NULL DEFAULT FALSE,
 state VARCHAR(32) NOT NULL DEFAULT 'waiting',
 checked_at DATETIME(6) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS trial_watches (
 id VARCHAR(32) PRIMARY KEY,
 site_id VARCHAR(64) NOT NULL,
 config_json TEXT NOT NULL,
 revision BIGINT NOT NULL DEFAULT 1,
 KEY idx_trial_site(site_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS trial_events (
 id VARCHAR(32) PRIMARY KEY,
 site_id VARCHAR(64) NOT NULL,
 watch_id VARCHAR(32) NOT NULL,
 round_id BIGINT NOT NULL,
 log_id BIGINT NOT NULL,
 payload_json TEXT NOT NULL,
 followed_by VARCHAR(128) NOT NULL DEFAULT '',
 created_at DATETIME(6) NOT NULL,
 UNIQUE KEY uq_trial_event(watch_id,round_id,log_id),
 KEY idx_trial_events_site(site_id,created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS trial_deliveries (
 id VARCHAR(32) PRIMARY KEY,
 event_id VARCHAR(32) NOT NULL,
 site_id VARCHAR(64) NOT NULL,
 log_id BIGINT NOT NULL,
 person_id VARCHAR(32) NOT NULL DEFAULT '',
 recipient_name VARCHAR(80) NOT NULL DEFAULT '',
 phone VARCHAR(32) NOT NULL DEFAULT '',
 kind VARCHAR(16) NOT NULL,
 status VARCHAR(32) NOT NULL DEFAULT 'pending',
 call_id VARCHAR(15) NOT NULL DEFAULT '',
 result_code VARCHAR(128) NOT NULL DEFAULT '',
 created_at DATETIME(6) NOT NULL,
 UNIQUE KEY uq_trial_delivery(site_id,log_id,kind,person_id),
 KEY idx_trial_delivery_pending(status,created_at),
 KEY idx_trial_delivery_event(event_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
