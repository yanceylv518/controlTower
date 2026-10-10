CREATE TABLE IF NOT EXISTS billing_upstream_urls (
  instance_id VARCHAR(64) NOT NULL,
  url_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  url VARCHAR(2048) NOT NULL,
  upstream_id BIGINT NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (instance_id,url_hash),
  KEY idx_billing_upstream_urls (upstream_id),
  CONSTRAINT fk_billing_upstream_url FOREIGN KEY (upstream_id) REFERENCES billing_upstreams(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
