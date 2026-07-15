CREATE INDEX idx_metric_1m_dim_bucket ON metric_1m (dimension_type, instance_id, dimension_key, bucket_time);
CREATE INDEX idx_metric_5m_dim_bucket ON metric_5m (dimension_type, instance_id, dimension_key, bucket_time);
