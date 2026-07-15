-- v2.3-B3 performance dataset: 2 instances x 60 dimensions x 7 days x 1-minute buckets = 1,209,600 rows.
-- Run only in a disposable benchmark database.
SET @anchor = UTC_TIMESTAMP() - INTERVAL 7 DAY;

INSERT INTO metric_1m (
  instance_id, bucket_time, dimension_type, dimension_key,
  request_count, success_count, error_count, success_rate, error_rate,
  tpm, prompt_tokens, completion_tokens, quota, avg_use_time, p95_use_time,
  updated_at
)
SELECT
  CONCAT('perf-', 1 + FLOOR(n / 604800)),
  @anchor + INTERVAL MOD(n, 10080) MINUTE,
  'instance_channel',
  CONCAT('perf-', 1 + FLOOR(n / 604800), ':channel:', 1 + FLOOR(MOD(n, 604800) / 10080)),
  10, 9, 1, 0.9, 0.1, 100, 1000, 500, 1500, 0.8, 1.5,
  UTC_TIMESTAMP()
FROM (
  SELECT a.n + 10*b.n + 100*c.n + 1000*d.n + 10000*e.n + 100000*f.n + 1000000*g.n AS n
  FROM
    (SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9) a
  CROSS JOIN (SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9) b
  CROSS JOIN (SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9) c
  CROSS JOIN (SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9) d
  CROSS JOIN (SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9) e
  CROSS JOIN (SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9) f
  CROSS JOIN (SELECT 0 n UNION ALL SELECT 1) g
) numbers
WHERE n < 1209600;

ANALYZE TABLE metric_1m;

EXPLAIN SELECT m.* FROM metric_1m m JOIN (
  SELECT instance_id, dimension_type, dimension_key, MAX(bucket_time) AS mb
  FROM metric_1m
  WHERE bucket_time >= UTC_TIMESTAMP() - INTERVAL 24 HOUR
    AND dimension_type = 'instance_channel'
  GROUP BY instance_id, dimension_type, dimension_key
) t ON m.instance_id=t.instance_id AND m.dimension_type=t.dimension_type
 AND m.dimension_key=t.dimension_key AND m.bucket_time=t.mb
LIMIT 5000;

EXPLAIN SELECT * FROM metric_1m
WHERE dimension_type='instance_channel'
  AND dimension_key='perf-1:channel:1'
  AND bucket_time >= UTC_TIMESTAMP() - INTERVAL 1 HOUR
ORDER BY bucket_time ASC;
