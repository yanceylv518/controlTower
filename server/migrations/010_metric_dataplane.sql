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

ALTER TABLE metric_1m ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
ALTER TABLE metric_5m ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
ALTER TABLE channel_snapshots ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
