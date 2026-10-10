ALTER TABLE billing_upstreams ADD COLUMN revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE billing_upstream_channel_bindings ADD COLUMN association_source VARCHAR(16) NOT NULL DEFAULT 'legacy';
ALTER TABLE billing_upstream_channel_bindings ADD COLUMN matched_prefix VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '';
ALTER TABLE billing_upstream_channel_bindings ADD COLUMN associated_at DATETIME(6) NULL;
ALTER TABLE billing_upstream_channel_bindings ADD COLUMN associated_by VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE billing_upstream_channel_exclusions ADD COLUMN channel_name VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE billing_upstream_channel_exclusions ADD COLUMN previous_upstream_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE billing_upstream_channel_exclusions ADD COLUMN updated_by VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE billing_upstream_prefixes ADD COLUMN needs_review TINYINT(1) NOT NULL DEFAULT 1;
UPDATE billing_upstream_prefixes p JOIN billing_upstreams u ON u.id=p.upstream_id SET p.needs_review=0 WHERE BINARY p.prefix=BINARY TRIM(u.name);
ALTER TABLE billing_upstream_urls DROP PRIMARY KEY, ADD PRIMARY KEY(instance_id,upstream_id,url_hash);
CREATE TABLE IF NOT EXISTS billing_upstream_sync_state (
 instance_id VARCHAR(64) NOT NULL PRIMARY KEY,
 synced_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
