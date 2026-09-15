-- NULL denotes legacy/missing evidence. Never copy public TTFT into these columns.
ALTER TABLE metric_1m
 ADD COLUMN speed_ttft2_le_250ms BIGINT NULL,
 ADD COLUMN speed_ttft2_le_500ms BIGINT NULL,
 ADD COLUMN speed_ttft2_le_1s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_2s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_3s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_5s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_8s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_10s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_12s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_20s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_30s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_45s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_60s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_90s BIGINT NULL,
 ADD COLUMN speed_ttft2_gt_90s BIGINT NULL,
 ADD COLUMN speed_retry_count BIGINT NULL,
 ADD COLUMN speed_unknown_count BIGINT NULL;

ALTER TABLE metric_5m
 ADD COLUMN speed_ttft2_le_250ms BIGINT NULL,
 ADD COLUMN speed_ttft2_le_500ms BIGINT NULL,
 ADD COLUMN speed_ttft2_le_1s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_2s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_3s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_5s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_8s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_10s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_12s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_20s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_30s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_45s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_60s BIGINT NULL,
 ADD COLUMN speed_ttft2_le_90s BIGINT NULL,
 ADD COLUMN speed_ttft2_gt_90s BIGINT NULL,
 ADD COLUMN speed_retry_count BIGINT NULL,
 ADD COLUMN speed_unknown_count BIGINT NULL;

ALTER TABLE tuning_continuous_states
 ADD COLUMN speed_sample_count BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN speed_retry_count BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN speed_unknown_count BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN speed_legacy_count BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN speed_stats_version INT NOT NULL DEFAULT 0;
