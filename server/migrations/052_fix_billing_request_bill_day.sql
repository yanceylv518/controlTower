-- billing_request_details.bill_day is a DATE. Older writers bound Shanghai
-- midnight as time.Time and the UTC driver stored the previous calendar day.
UPDATE billing_request_details
SET bill_day=DATE(CONVERT_TZ(created_at,'+00:00','+08:00'))
WHERE bill_day<>DATE(CONVERT_TZ(created_at,'+00:00','+08:00'));

DELETE FROM billing_user_daily_active;

INSERT INTO billing_user_daily_active(instance_id,bill_day,user_id,job_id,activated_at)
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
),day_status.anomaly_requests=(
  SELECT COUNT(*)
  FROM billing_user_daily_active active
  JOIN billing_anomaly_orders anomaly
    ON anomaly.instance_id=active.instance_id
   AND anomaly.user_id=active.user_id
   AND anomaly.job_id=active.job_id
   AND DATE(CONVERT_TZ(anomaly.created_at,'+00:00','+08:00'))=active.bill_day
  WHERE active.instance_id=day_status.instance_id AND active.bill_day=day_status.bill_day
);
