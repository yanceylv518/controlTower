CREATE INDEX idx_channel_commands_expiry ON channel_commands (status, created_at, id);
