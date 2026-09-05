-- base_priority_sync exceeds the original 16-character duty-rule limit.
ALTER TABLE tuning_recommendations MODIFY COLUMN rule VARCHAR(64) NOT NULL;
