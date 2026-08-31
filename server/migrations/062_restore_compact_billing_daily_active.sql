-- Restore active user/day pointers that may have been removed by historical
-- migration 052 during a Server restart. Only completed generate jobs with
-- compact totals are candidates. Existing active pointers are authoritative
-- and are never overwritten.
INSERT IGNORE INTO billing_user_daily_active(instance_id,bill_day,user_id,job_id,activated_at)
SELECT ranked.instance_id,ranked.bill_day,ranked.user_id,ranked.job_id,ranked.activated_at
FROM (
  SELECT candidates.*,
    ROW_NUMBER() OVER (
      PARTITION BY candidates.instance_id,candidates.bill_day,candidates.user_id
      ORDER BY candidates.activated_at DESC,candidates.job_id DESC
    ) AS row_no
  FROM (
    SELECT DISTINCT compact.instance_id,compact.bill_day,compact.user_id,compact.job_id,
      COALESCE(jobs.finished_at,jobs.updated_at) AS activated_at
    FROM billing_compact_daily_totals compact
    JOIN billing_jobs jobs
      ON jobs.id=compact.job_id
     AND jobs.instance_id=compact.instance_id
     AND jobs.job_type='generate'
     AND jobs.status='complete'
  ) candidates
) ranked
WHERE ranked.row_no=1;
