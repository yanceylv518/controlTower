-- 旧指标无法可靠还原消费请求数，保留 NULL 表示未知。
ALTER TABLE metric_1m ADD COLUMN consume_count BIGINT NULL;
ALTER TABLE metric_5m ADD COLUMN consume_count BIGINT NULL;
