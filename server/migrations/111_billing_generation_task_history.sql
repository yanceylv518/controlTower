CREATE TABLE IF NOT EXISTS billing_generation_tasks (
 id VARCHAR(64) NOT NULL PRIMARY KEY,
 instance_id VARCHAR(64) NOT NULL,
 kind VARCHAR(32) NOT NULL,
 range_from DATETIME(6) NOT NULL,
 range_to DATETIME(6) NOT NULL,
 work_until DATETIME(6) NOT NULL,
 source VARCHAR(24) NOT NULL,
 overwrite_existing TINYINT(1) NOT NULL DEFAULT 0,
 subject_ids_json JSON NOT NULL,
 job_id VARCHAR(64) NOT NULL DEFAULT '',
 progress_json JSON NULL,
 created_at DATETIME(6) NOT NULL,
 INDEX idx_billing_task_history(instance_id,created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT IGNORE INTO billing_generation_tasks(id,instance_id,kind,range_from,range_to,work_until,source,overwrite_existing,subject_ids_json,created_at)
SELECT batch_id,instance_id,kind,CONVERT_TZ(range_from,'+08:00','+00:00'),CONVERT_TZ(range_to,'+08:00','+00:00'),LEAST(CONVERT_TZ(range_to,'+08:00','+00:00'),CONVERT_TZ(DATE(CONVERT_TZ(COALESCE(MAX(generation_started_at),MAX(created_at)),'+00:00','+08:00')),'+08:00','+00:00')),'legacy',MAX(overwrite_existing),JSON_ARRAYAGG(subject_id),COALESCE(MAX(generation_started_at),MIN(created_at)) FROM billing_generation_ranges WHERE batch_id<>'' AND kind='user_statement' GROUP BY batch_id,instance_id,kind,range_from,range_to;
INSERT IGNORE INTO billing_generation_tasks(id,instance_id,kind,range_from,range_to,work_until,source,subject_ids_json,job_id,created_at)
SELECT CONCAT('job:',j.id),j.instance_id,j.job_type,j.range_from,j.range_to,j.range_to,IF(j.bill_period='temporary','temporary','automatic'),JSON_ARRAY(j.user_id),j.id,j.created_at FROM billing_jobs j WHERE j.job_type='user_statement' AND j.usage_version>=3 AND NOT EXISTS (SELECT 1 FROM billing_generation_tasks t WHERE t.instance_id=j.instance_id AND t.kind=j.job_type AND JSON_CONTAINS(t.subject_ids_json,CAST(j.user_id AS JSON)) AND j.range_from>=t.range_from AND j.range_to<=t.work_until);
