ALTER TABLE nginx_slow_samples ADD COLUMN request_id VARCHAR(255) NULL AFTER bytes;
CREATE INDEX idx_nginx_slow_samples_instance_request ON nginx_slow_samples (instance_id, request_id);
