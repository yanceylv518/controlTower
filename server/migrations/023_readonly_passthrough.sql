ALTER TABLE instances
  ADD COLUMN logs_readonly_dsn VARCHAR(768) NOT NULL DEFAULT '' AFTER base_url;
