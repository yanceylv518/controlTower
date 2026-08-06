ALTER TABLE billing_anomaly_orders
  DROP PRIMARY KEY,
  ADD PRIMARY KEY (job_id, instance_id, source_log_id);
