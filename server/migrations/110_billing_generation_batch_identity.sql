ALTER TABLE billing_generation_ranges ADD COLUMN batch_id VARCHAR(64) NOT NULL DEFAULT '';
-- Legacy registrations were inserted sequentially by a single transaction.
-- Retain a separate legacy identity and all future submissions use random batch IDs.
UPDATE billing_generation_ranges SET batch_id=SHA2(CONCAT(instance_id,'|',kind,'|',range_from,'|',range_to,'|',DATE_FORMAT(created_at,'%Y-%m-%d %H:%i:%s')),256) WHERE batch_id='';
CREATE INDEX idx_billing_generation_batch ON billing_generation_ranges(instance_id,batch_id);
