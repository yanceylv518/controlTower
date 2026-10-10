CREATE TABLE IF NOT EXISTS billing_upstream_prefixes (
 instance_id VARCHAR(64) NOT NULL,
 prefix VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 upstream_id BIGINT NOT NULL,
 PRIMARY KEY(instance_id,prefix),
 KEY idx_upstream_prefix_owner(upstream_id),
 CONSTRAINT fk_upstream_prefix_owner FOREIGN KEY(upstream_id) REFERENCES billing_upstreams(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO billing_upstream_prefixes(instance_id,prefix,upstream_id)
SELECT instance_id,TRIM(name),id FROM billing_upstreams
WHERE TRIM(name)<>'' AND CHAR_LENGTH(TRIM(name))<=128
ON DUPLICATE KEY UPDATE prefix=prefix;
