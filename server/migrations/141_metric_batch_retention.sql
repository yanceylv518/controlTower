-- Existing batches have no verifiable expiry and remain untouched.
ALTER TABLE metric_batches ADD COLUMN expires_at DATETIME(6) NULL;
CREATE INDEX idx_metric_batches_expires ON metric_batches (expires_at);
