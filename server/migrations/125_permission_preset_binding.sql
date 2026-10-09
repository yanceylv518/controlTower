ALTER TABLE users
    ADD COLUMN permission_preset_id BIGINT NOT NULL DEFAULT 0;

ALTER TABLE users
    ADD INDEX idx_users_permission_preset (permission_preset_id);
