-- 兼容旧本地计划表，重复建表保留既有配置与执行记录。
CREATE TABLE IF NOT EXISTS channel_schedules (
  site_id VARCHAR(191) NOT NULL,
  channel_id BIGINT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT FALSE,
  config_json JSON NOT NULL,
  revision BIGINT NOT NULL DEFAULT 1,
  effective_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(128) NOT NULL,
  closed_by_schedule BOOLEAN NOT NULL DEFAULT FALSE,
  last_execution_id VARCHAR(64) NOT NULL DEFAULT '',
  next_check_at DATETIME(6) NOT NULL,
  PRIMARY KEY (site_id, channel_id),
  KEY idx_channel_schedules_due (enabled, next_check_at)
);

CREATE TABLE IF NOT EXISTS channel_schedule_executions (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  site_id VARCHAR(191) NOT NULL,
  channel_id BIGINT NOT NULL,
  revision BIGINT NOT NULL,
  boundary_at DATETIME(6) NOT NULL,
  target_status INT NOT NULL,
  status VARCHAR(32) NOT NULL,
  attempts INT NOT NULL DEFAULT 0,
  error_summary VARCHAR(512) NOT NULL DEFAULT '',
  before_status INT NOT NULL DEFAULT 0,
  expires_at DATETIME(6) NULL,
  updated_at DATETIME(6) NOT NULL,
  UNIQUE KEY idx_channel_schedule_boundary (site_id, channel_id, revision, boundary_at),
  KEY idx_channel_schedule_history (site_id, channel_id, updated_at)
);

CREATE TABLE IF NOT EXISTS channel_control_site_guards (
  site_id VARCHAR(191) NOT NULL PRIMARY KEY
);
