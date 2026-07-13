CREATE TABLE IF NOT EXISTS alert_events (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  alert_id VARCHAR(64) NOT NULL,
  event_type VARCHAR(32) NOT NULL,
  actor VARCHAR(64) NOT NULL,
  note VARCHAR(500) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL,
  INDEX idx_alert_events_alert (alert_id, created_at)
);
