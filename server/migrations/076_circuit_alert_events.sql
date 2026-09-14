CREATE TABLE IF NOT EXISTS circuit_alert_events (
  event_id VARCHAR(64) NOT NULL PRIMARY KEY,
  alert_id VARCHAR(64) NOT NULL,
  site_id VARCHAR(128) NOT NULL,
  channel_id BIGINT NOT NULL,
  rule_key VARCHAR(64) NOT NULL,
  occurred_at DATETIME(3) NOT NULL,
  INDEX idx_circuit_alert_channel (site_id, channel_id, occurred_at),
  INDEX idx_circuit_alert_id (alert_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- A persistent activation boundary prevents replaying pre-upgrade history.
INSERT IGNORE INTO circuit_alert_events
  (event_id,alert_id,site_id,channel_id,rule_key,occurred_at)
VALUES ('__activation__','','',0,'',UTC_TIMESTAMP(3));
