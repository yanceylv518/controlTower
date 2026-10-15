-- One-time delivery boundary: retain history, but never automatically replay
-- events preceding this upgrade. INSERT IGNORE makes the boundary restart-safe.
INSERT IGNORE INTO circuit_alert_events
  (event_id,alert_id,site_id,channel_id,rule_key,occurred_at)
VALUES ('__notification_cutoff__','','',0,'',UTC_TIMESTAMP(3));
