ALTER TABLE billing_anomaly_orders
  ADD COLUMN actual_amount DECIMAL(18,6) NOT NULL DEFAULT 0 AFTER reference_amount;
