ALTER TABLE instances
  ADD COLUMN control_api_url VARCHAR(255) NOT NULL DEFAULT '' AFTER logs_readonly_dsn,
  ADD COLUMN control_api_token VARCHAR(1024) NOT NULL DEFAULT '' AFTER control_api_url,
  ADD COLUMN control_admin_user_id BIGINT NOT NULL DEFAULT 0 AFTER control_api_token;
