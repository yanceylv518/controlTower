ALTER TABLE metric_1m
  ADD COLUMN p50_use_time DOUBLE NULL AFTER avg_use_time,
  ADD COLUMN p99_use_time DOUBLE NULL AFTER p95_use_time,
  ADD COLUMN big_input_count BIGINT NULL AFTER cache_prompt_tokens,
  ADD COLUMN big_input_cache_hits BIGINT NULL AFTER big_input_count,
  ADD COLUMN ttft_count BIGINT NULL AFTER big_input_cache_hits,
  ADD COLUMN ttft_sum_ms BIGINT NULL AFTER ttft_count,
  ADD COLUMN ttft_p95_ms DOUBLE NULL AFTER ttft_sum_ms;

ALTER TABLE metric_5m
  ADD COLUMN p50_use_time DOUBLE NULL AFTER avg_use_time,
  ADD COLUMN p99_use_time DOUBLE NULL AFTER p95_use_time,
  ADD COLUMN big_input_count BIGINT NULL AFTER cache_prompt_tokens,
  ADD COLUMN big_input_cache_hits BIGINT NULL AFTER big_input_count,
  ADD COLUMN ttft_count BIGINT NULL AFTER big_input_cache_hits,
  ADD COLUMN ttft_sum_ms BIGINT NULL AFTER ttft_count,
  ADD COLUMN ttft_p95_ms DOUBLE NULL AFTER ttft_sum_ms;

ALTER TABLE channel_snapshots
  ADD COLUMN group_name VARCHAR(128) NULL AFTER models_text,
  ADD COLUMN priority BIGINT NULL AFTER group_name;

-- 注意：不要在此追加对表引擎/字符集的"重钉" ALTER 语句——
-- ApplyDir 每次启动重放所有迁移文件,该类语句每次都会成功执行并强制全表重建。
-- 三张表的引擎与排序规则已在 001_init.sql 建表时钉定。
