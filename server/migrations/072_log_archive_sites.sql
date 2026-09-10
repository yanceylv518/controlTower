CREATE TABLE IF NOT EXISTS site_log_archive_control (
 site_id VARCHAR(64) NOT NULL PRIMARY KEY,
 config_json JSON NOT NULL,
 status_json JSON NOT NULL,
 seen_at DATETIME(6) NULL,
 session_id VARCHAR(32) NOT NULL DEFAULT '',
 lease_until DATETIME(6) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS log_archive_targets (
 instance_id VARCHAR(64) NOT NULL,
 agent_id VARCHAR(64) NOT NULL,
 configured TINYINT(1) NOT NULL,
 seen_at DATETIME(6) NOT NULL,
 PRIMARY KEY(instance_id,agent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Merge old instance policies into a paused site policy. Never auto-enable two
-- agents on a shared source. Preserve the maximum old grant while it drains.
INSERT INTO site_log_archive_control(site_id,config_json,status_json,session_id,lease_until)
SELECT COALESCE(NULLIF(i.site_id,''),i.id),
 JSON_OBJECT('version',MAX(CAST(JSON_UNQUOTE(JSON_EXTRACT(a.config_json,'$.version')) AS UNSIGNED))+1,
 'instance_id','','agent_id','','running',JSON_EXTRACT('false','$'),'batch_size',500,'interval_seconds',30,'delay_seconds',300),
 '{}','migration',MAX(a.lease_until)
FROM log_archive_control a JOIN instances i ON i.id=a.instance_id AND i.deleted=0
GROUP BY COALESCE(NULLIF(i.site_id,''),i.id)
ON DUPLICATE KEY UPDATE site_id=VALUES(site_id);
