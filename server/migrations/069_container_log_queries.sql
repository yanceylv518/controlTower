CREATE TABLE IF NOT EXISTS container_log_targets (
 instance_id VARCHAR(128) NOT NULL,
 agent_id VARCHAR(128) NOT NULL,
 containers_json TEXT NOT NULL,
 seen_at DATETIME(6) NOT NULL,
 PRIMARY KEY(instance_id,agent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE IF NOT EXISTS container_log_tasks (
 id VARCHAR(64) NOT NULL PRIMARY KEY,
 instance_id VARCHAR(128) NOT NULL,
 agent_id VARCHAR(128) NOT NULL,
 actor_id BIGINT NOT NULL,
 actor VARCHAR(128) NOT NULL,
 actor_name VARCHAR(128) NOT NULL,
 query_json TEXT NOT NULL,
 status VARCHAR(24) NOT NULL,
 result_json MEDIUMTEXT NOT NULL,
 created_at DATETIME(6) NOT NULL,
 claimed_at DATETIME(6) NULL,
 INDEX idx_clog_owner(actor_id,created_at),
 INDEX idx_clog_created(created_at),
 INDEX idx_clog_claim(instance_id,agent_id,status,created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
