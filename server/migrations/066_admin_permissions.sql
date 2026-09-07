ALTER TABLE users ADD COLUMN display_name VARCHAR(64) NOT NULL DEFAULT '';
-- NULL preserves existing administrators. Newly created administrators use an explicit JSON array.
ALTER TABLE users ADD COLUMN permissions TEXT NULL;
