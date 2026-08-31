-- Restore the active per-user pointers for ledgers generated before
-- billing_user_daily_active was introduced. Empty scheduler runs are excluded
-- because they have no request details and must not hide an older valid bill.
INSERT IGNORE INTO billing_user_daily_active(instance_id,bill_day,user_id,job_id,activated_at)
SELECT ranked.instance_id,ranked.bill_day,ranked.user_id,ranked.job_id,ranked.updated_at
FROM (
  SELECT candidates.*,
    ROW_NUMBER() OVER (
      PARTITION BY candidates.instance_id,candidates.bill_day,candidates.user_id
      ORDER BY candidates.updated_at DESC,candidates.job_id DESC
    ) AS row_no
  FROM (
    SELECT DISTINCT details.instance_id,details.bill_day,details.user_id,details.job_id,jobs.updated_at
    FROM billing_request_details details
    JOIN billing_jobs jobs ON jobs.id=details.job_id AND jobs.status='complete'
  ) candidates
) ranked
-- Never replace a pointer already published by the compact billing workflow.
-- This migration may run once more while an installation adopts
-- schema_migrations, so only fill pointers that are genuinely missing.
WHERE ranked.row_no=1;

UPDATE billing_day_status day_status
SET day_status.normal_requests=(
  SELECT COUNT(*)
  FROM billing_user_daily_active active
  JOIN billing_request_details details
    ON details.instance_id=active.instance_id
   AND details.bill_day=active.bill_day
   AND details.user_id=active.user_id
   AND details.job_id=active.job_id
  WHERE active.instance_id=day_status.instance_id AND active.bill_day=day_status.bill_day
),day_status.status='complete'
WHERE EXISTS (
  SELECT 1
  FROM billing_request_details details
  WHERE details.instance_id=day_status.instance_id
    AND details.bill_day=day_status.bill_day
    AND details.job_id=day_status.active_job_id
);
