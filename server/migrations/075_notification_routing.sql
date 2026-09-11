ALTER TABLE notification_channels
 ADD COLUMN site_id VARCHAR(128) NOT NULL DEFAULT '',
 ADD INDEX idx_notification_channels_site (site_id),
 ADD COLUMN rule_keys JSON NULL;
