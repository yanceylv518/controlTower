CREATE TABLE IF NOT EXISTS billing_upstreams (
  id BIGINT NOT NULL AUTO_INCREMENT,
  instance_id VARCHAR(64) NOT NULL,
  name VARCHAR(128) NOT NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  remark VARCHAR(500) NOT NULL DEFAULT '',
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(128) NOT NULL DEFAULT '',
  PRIMARY KEY (id),
  UNIQUE KEY uq_billing_upstream_name (instance_id, name),
  KEY idx_billing_upstreams_instance (instance_id, enabled)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_upstream_channel_bindings (
  instance_id VARCHAR(64) NOT NULL,
  upstream_id BIGINT NOT NULL,
  channel_id BIGINT NOT NULL,
  channel_name VARCHAR(128) NOT NULL DEFAULT '',
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (instance_id, channel_id),
  KEY idx_billing_upstream_binding (instance_id, upstream_id),
  CONSTRAINT fk_billing_upstream_binding FOREIGN KEY (upstream_id) REFERENCES billing_upstreams(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
