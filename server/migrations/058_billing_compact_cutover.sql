-- Do not perform a startup-blocking full scan of the legacy request ledger.
-- Existing non-empty days without compact totals are made explicitly
-- incomplete and can be regenerated through the normal resumable job path.
UPDATE billing_day_status status
SET status.status='needs_regeneration'
WHERE status.normal_requests>0
  AND NOT EXISTS (
    SELECT 1 FROM billing_compact_daily_totals compact
    WHERE compact.job_id=status.active_job_id
      AND compact.instance_id=status.instance_id
      AND compact.bill_day=status.bill_day
  );

DELETE active
FROM billing_user_daily_active active
WHERE NOT EXISTS (
  SELECT 1 FROM billing_compact_daily_totals compact
  WHERE compact.job_id=active.job_id
    AND compact.instance_id=active.instance_id
    AND compact.bill_day=active.bill_day
    AND compact.user_id=active.user_id
);
