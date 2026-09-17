-- Separate total-request-time output speed from legacy generation-time OTPS.
-- Historical aggregates cannot be safely reconstructed. Leave them NULL.
ALTER TABLE metric_1m
 ADD COLUMN output_speed_tokens BIGINT NULL,
 ADD COLUMN output_speed_seconds DOUBLE NULL,
 ADD COLUMN output_speed_samples BIGINT NULL,
 ADD COLUMN output_direct_tokens BIGINT NULL,
 ADD COLUMN output_direct_seconds DOUBLE NULL,
 ADD COLUMN output_direct_samples BIGINT NULL,
 ADD COLUMN output_retry_samples BIGINT NULL,
 ADD COLUMN output_unknown_samples BIGINT NULL;

ALTER TABLE metric_5m
 ADD COLUMN output_speed_tokens BIGINT NULL,
 ADD COLUMN output_speed_seconds DOUBLE NULL,
 ADD COLUMN output_speed_samples BIGINT NULL,
 ADD COLUMN output_direct_tokens BIGINT NULL,
 ADD COLUMN output_direct_seconds DOUBLE NULL,
 ADD COLUMN output_direct_samples BIGINT NULL,
 ADD COLUMN output_retry_samples BIGINT NULL,
 ADD COLUMN output_unknown_samples BIGINT NULL;

ALTER TABLE tuning_continuous_states
 ADD COLUMN otps_sample_count BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN otps_retry_count BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN otps_unknown_count BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN otps_stats_version INT NOT NULL DEFAULT 0;
