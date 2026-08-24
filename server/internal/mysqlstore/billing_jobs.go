package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"strings"
	"time"
)

func (s Store) CreateBillingJob(ctx context.Context, j billing.Job, steps []billing.JobStep) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	e = createBillingJobTx(ctx, tx, j, steps)
	if e != nil {
		return e
	}
	return tx.Commit()
}

func createBillingJobTx(ctx context.Context, tx *sql.Tx, j billing.Job, steps []billing.JobStep) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO billing_jobs(id,request_key,instance_id,job_type,user_id,range_from,range_to,status,total_steps,requested_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, j.ID, nullBillingRequestKey(j.RequestKey), j.InstanceID, j.JobType, j.UserID, j.From, j.To, j.Status, j.TotalSteps, j.RequestedBy, j.CreatedAt, j.UpdatedAt); err != nil {
		return err
	}
	for _, v := range steps {
		if _, err := tx.ExecContext(ctx, `INSERT INTO billing_job_steps(job_id,step_no,range_from,range_to,status,updated_at) VALUES(?,?,?,?,?,?)`, v.JobID, v.StepNo, v.From, v.To, "pending", j.UpdatedAt); err != nil {
			return err
		}
	}
	// User 0 is a marker proving that the snapshot exists even when the site
	// has no explicit per-user overrides. Actual new-api user IDs are positive.
	if _, err := tx.ExecContext(ctx, `INSERT INTO billing_job_user_settings(job_id,user_id,use_tiered_pricing) VALUES(?,0,1)`, j.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO billing_job_user_settings(job_id,user_id,use_tiered_pricing) SELECT ?,user_id,use_tiered_pricing FROM billing_user_settings WHERE instance_id=?`, j.ID, j.InstanceID); err != nil {
		return err
	}
	return nil
}

func (s Store) CreateBillingVerificationJob(ctx context.Context, j billing.Job, steps []billing.JobStep, sourceJobID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize creation on the immutable source job. This closes the race
	// between two clients that both observed "no verification job".
	var lockedSourceID string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM billing_jobs WHERE id=? FOR UPDATE`, sourceJobID).Scan(&lockedSourceID); err != nil {
		return err
	}
	var existingID string
	err = tx.QueryRowContext(ctx, `SELECT v.job_id FROM billing_verification_jobs v JOIN billing_jobs j ON j.id=v.job_id WHERE v.source_job_id=? AND j.status<>'failed' ORDER BY j.created_at DESC LIMIT 1`, sourceJobID).Scan(&existingID)
	if err == nil {
		return billing.ErrVerificationAlreadyExists
	}
	if err != sql.ErrNoRows {
		return err
	}
	if err = createBillingJobTx(ctx, tx, j, steps); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO billing_verification_jobs(job_id,source_job_id) VALUES(?,?)`, j.ID, sourceJobID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) VerificationSourceJob(ctx context.Context, jobID string) (billing.Job, error) {
	var sourceID string
	if err := s.db.QueryRowContext(ctx, `SELECT source_job_id FROM billing_verification_jobs WHERE job_id=?`, jobID).Scan(&sourceID); err != nil {
		return billing.Job{}, err
	}
	return s.BillingJob(ctx, sourceID)
}

func (s Store) LatestBillingVerificationJob(ctx context.Context, sourceJobID string) (billing.Job, error) {
	var id string
	if err := s.db.QueryRowContext(ctx, `SELECT v.job_id FROM billing_verification_jobs v JOIN billing_jobs j ON j.id=v.job_id WHERE v.source_job_id=? ORDER BY j.created_at DESC LIMIT 1`, sourceJobID).Scan(&id); err != nil {
		return billing.Job{}, err
	}
	return s.BillingJob(ctx, id)
}
func nullBillingRequestKey(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func (s Store) BillingJobByRequestKey(ctx context.Context, key string) (billing.Job, error) {
	var j billing.Job
	e := s.db.QueryRowContext(ctx, `SELECT id,COALESCE(request_key,''),instance_id,job_type,user_id,range_from,range_to,status,total_steps,completed_steps,abnormal_rows,error_message,output_path,requested_by,created_at,updated_at FROM billing_jobs WHERE request_key=?`, key).Scan(&j.ID, &j.RequestKey, &j.InstanceID, &j.JobType, &j.UserID, &j.From, &j.To, &j.Status, &j.TotalSteps, &j.CompletedSteps, &j.AbnormalRows, &j.ErrorMessage, &j.OutputPath, &j.RequestedBy, &j.CreatedAt, &j.UpdatedAt)
	return j, e
}

func (s Store) ActiveBillingJob(ctx context.Context) (billing.Job, error) {
	var j billing.Job
	err := s.db.QueryRowContext(ctx, `SELECT id,instance_id,job_type,user_id,range_from,range_to,status,total_steps,completed_steps,abnormal_rows,error_message,output_path,requested_by,created_at,updated_at FROM billing_jobs WHERE status IN ('pending','running') ORDER BY created_at LIMIT 1`).Scan(&j.ID, &j.InstanceID, &j.JobType, &j.UserID, &j.From, &j.To, &j.Status, &j.TotalSteps, &j.CompletedSteps, &j.AbnormalRows, &j.ErrorMessage, &j.OutputPath, &j.RequestedBy, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

func (s Store) CancelBillingJob(ctx context.Context, id string) error {
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE billing_job_steps SET status='failed',error_message='cancelled manually',finished_at=?,updated_at=? WHERE job_id=? AND status IN ('pending','running')`, now, now, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE billing_jobs SET status='failed',error_message='cancelled manually',finished_at=?,updated_at=? WHERE id=? AND status IN ('pending','running')`, now, now, id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s Store) ListBillingJobs(ctx context.Context, instanceID, status string, limit int) ([]billing.Job, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := `SELECT id,instance_id,job_type,user_id,range_from,range_to,status,total_steps,completed_steps,abnormal_rows,error_message,output_path,requested_by,created_at,updated_at FROM billing_jobs`
	args := []any{}
	conditions := []string{}
	if instanceID != "" {
		conditions = append(conditions, `instance_id=?`)
		args = append(args, instanceID)
	}
	if status != "" {
		conditions = append(conditions, `status=?`)
		args = append(args, status)
	}
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	query += ` ORDER BY FIELD(status,'running','pending','failed','complete'),created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.Job{}
	for rows.Next() {
		var j billing.Job
		if err = rows.Scan(&j.ID, &j.InstanceID, &j.JobType, &j.UserID, &j.From, &j.To, &j.Status, &j.TotalSteps, &j.CompletedSteps, &j.AbnormalRows, &j.ErrorMessage, &j.OutputPath, &j.RequestedBy, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, j)
	}
	return items, rows.Err()
}

func (s Store) ClaimBillingStep(ctx context.Context) (billing.Job, billing.JobStep, bool, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return billing.Job{}, billing.JobStep{}, false, e
	}
	defer tx.Rollback()
	var j billing.Job
	var st billing.JobStep
	e = tx.QueryRowContext(ctx, `SELECT j.id,j.instance_id,j.job_type,j.user_id,j.range_from,j.range_to,j.status,j.total_steps,j.completed_steps,j.abnormal_rows,j.error_message,j.output_path,j.requested_by,j.created_at,j.updated_at,s.step_no,s.range_from,s.range_to,s.cursor_created_at,s.cursor_id FROM billing_jobs j JOIN billing_job_steps s ON s.job_id=j.id WHERE j.status IN ('pending','running') AND s.status IN ('pending','running') ORDER BY j.created_at,s.step_no LIMIT 1 FOR UPDATE`).Scan(&j.ID, &j.InstanceID, &j.JobType, &j.UserID, &j.From, &j.To, &j.Status, &j.TotalSteps, &j.CompletedSteps, &j.AbnormalRows, &j.ErrorMessage, &j.OutputPath, &j.RequestedBy, &j.CreatedAt, &j.UpdatedAt, &st.StepNo, &st.From, &st.To, &st.Cursor.CreatedUnix, &st.Cursor.ID)
	if e == sql.ErrNoRows {
		return j, st, false, nil
	}
	if e != nil {
		return j, st, false, e
	}
	st.JobID = j.ID
	now := time.Now().UTC()
	if _, e = tx.ExecContext(ctx, `UPDATE billing_jobs SET status='running',started_at=COALESCE(started_at,?),updated_at=? WHERE id=?`, now, now, j.ID); e != nil {
		return j, st, false, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE billing_job_steps SET status='running',attempts=attempts+1,started_at=COALESCE(started_at,?),updated_at=? WHERE job_id=? AND step_no=?`, now, now, j.ID, st.StepNo); e != nil {
		return j, st, false, e
	}
	return j, st, true, tx.Commit()
}

func (s Store) BillingJob(ctx context.Context, id string) (billing.Job, error) {
	var j billing.Job
	e := s.db.QueryRowContext(ctx, `SELECT id,instance_id,job_type,user_id,range_from,range_to,status,total_steps,completed_steps,abnormal_rows,error_message,output_path,requested_by,created_at,updated_at FROM billing_jobs WHERE id=?`, id).Scan(&j.ID, &j.InstanceID, &j.JobType, &j.UserID, &j.From, &j.To, &j.Status, &j.TotalSteps, &j.CompletedSteps, &j.AbnormalRows, &j.ErrorMessage, &j.OutputPath, &j.RequestedBy, &j.CreatedAt, &j.UpdatedAt)
	return j, e
}
func (s Store) ListBillingUserSettings(ctx context.Context, site string) (map[int64]billing.UserSetting, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT instance_id,user_id,use_tiered_pricing,updated_at,updated_by FROM billing_user_settings WHERE instance_id=?`, site)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[int64]billing.UserSetting{}
	for rows.Next() {
		var v billing.UserSetting
		if e = rows.Scan(&v.InstanceID, &v.UserID, &v.UseTieredPricing, &v.UpdatedAt, &v.UpdatedBy); e != nil {
			return nil, e
		}
		out[v.UserID] = v
	}
	return out, rows.Err()
}

func (s Store) BillingUserSettingsForJob(ctx context.Context, jobID string) (map[int64]billing.UserSetting, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT user_id,use_tiered_pricing FROM billing_job_user_settings WHERE job_id=?`, jobID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[int64]billing.UserSetting{}
	for rows.Next() {
		var v billing.UserSetting
		if e = rows.Scan(&v.UserID, &v.UseTieredPricing); e != nil {
			return nil, e
		}
		out[v.UserID] = v
	}
	return out, rows.Err()
}
func (s Store) PutBillingUserSetting(ctx context.Context, v billing.UserSetting) error {
	_, e := s.db.ExecContext(ctx, `INSERT INTO billing_user_settings(instance_id,user_id,use_tiered_pricing,updated_at,updated_by) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE use_tiered_pricing=VALUES(use_tiered_pricing),updated_at=VALUES(updated_at),updated_by=VALUES(updated_by)`, v.InstanceID, v.UserID, v.UseTieredPricing, v.UpdatedAt, v.UpdatedBy)
	return e
}

func (s Store) AppendBillingHour(ctx context.Context, j billing.Job, st billing.JobStep, items []billing.DailyRow, tokens []billing.TokenDailyRow, channels []billing.ChannelDailyRow, bad []billing.AnomalyOrder, c billing.LogCursor, pageRows int64) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	for _, v := range items {
		_, e = tx.ExecContext(ctx, `INSERT INTO billing_hourly(job_id,instance_id,hour_start,user_id,username,model_name,group_name,tier_from,request_count,prompt_tokens,completion_tokens,cache_tokens,cache_write_tokens,cache_write_5m_tokens,cache_write_1h_tokens,quota,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE username=VALUES(username),request_count=request_count+VALUES(request_count),prompt_tokens=prompt_tokens+VALUES(prompt_tokens),completion_tokens=completion_tokens+VALUES(completion_tokens),cache_tokens=cache_tokens+VALUES(cache_tokens),cache_write_tokens=cache_write_tokens+VALUES(cache_write_tokens),cache_write_5m_tokens=cache_write_5m_tokens+VALUES(cache_write_5m_tokens),cache_write_1h_tokens=cache_write_1h_tokens+VALUES(cache_write_1h_tokens),quota=quota+VALUES(quota),updated_at=VALUES(updated_at)`, j.ID, j.InstanceID, st.From.UTC(), v.UserID, v.Username, v.ModelName, v.GroupName, v.TierFrom, v.RequestCount, v.PromptTokens, v.CompletionTokens, v.CacheTokens, v.CacheWriteTokens, v.CacheWrite5mTokens, v.CacheWrite1hTokens, v.Quota, now)
		if e != nil {
			return e
		}
	}
	for _, v := range tokens {
		_, e = tx.ExecContext(ctx, `INSERT INTO billing_token_daily_versions(job_id,instance_id,user_id,token_id,token_name,username,model_name,group_name,tier_from,day,request_count,prompt_tokens,completion_tokens,cache_tokens,cache_write_tokens,cache_write_5m_tokens,cache_write_1h_tokens,quota,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE token_name=VALUES(token_name),username=VALUES(username),request_count=request_count+VALUES(request_count),prompt_tokens=prompt_tokens+VALUES(prompt_tokens),completion_tokens=completion_tokens+VALUES(completion_tokens),cache_tokens=cache_tokens+VALUES(cache_tokens),cache_write_tokens=cache_write_tokens+VALUES(cache_write_tokens),cache_write_5m_tokens=cache_write_5m_tokens+VALUES(cache_write_5m_tokens),cache_write_1h_tokens=cache_write_1h_tokens+VALUES(cache_write_1h_tokens),quota=quota+VALUES(quota),updated_at=VALUES(updated_at)`, j.ID, v.InstanceID, v.UserID, v.TokenID, v.TokenName, v.Username, v.ModelName, v.GroupName, v.TierFrom, v.Day, v.RequestCount, v.PromptTokens, v.CompletionTokens, v.CacheTokens, v.CacheWriteTokens, v.CacheWrite5mTokens, v.CacheWrite1hTokens, v.Quota, now)
		if e != nil {
			return e
		}
	}
	for _, v := range channels {
		_, e = tx.ExecContext(ctx, `INSERT INTO billing_channel_hourly(job_id,instance_id,hour_start,channel_id,channel_name,model_name,group_name,tier_from,request_count,prompt_tokens,completion_tokens,cache_tokens,cache_write_tokens,cache_write_5m_tokens,cache_write_1h_tokens,quota,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE channel_name=VALUES(channel_name),request_count=request_count+VALUES(request_count),prompt_tokens=prompt_tokens+VALUES(prompt_tokens),completion_tokens=completion_tokens+VALUES(completion_tokens),cache_tokens=cache_tokens+VALUES(cache_tokens),cache_write_tokens=cache_write_tokens+VALUES(cache_write_tokens),cache_write_5m_tokens=cache_write_5m_tokens+VALUES(cache_write_5m_tokens),cache_write_1h_tokens=cache_write_1h_tokens+VALUES(cache_write_1h_tokens),quota=quota+VALUES(quota),updated_at=VALUES(updated_at)`, j.ID, j.InstanceID, st.From.UTC(), v.ChannelID, v.ChannelName, v.ModelName, v.GroupName, v.TierFrom, v.RequestCount, v.PromptTokens, v.CompletionTokens, v.CacheTokens, v.CacheWriteTokens, v.CacheWrite5mTokens, v.CacheWrite1hTokens, v.Quota, now)
		if e != nil {
			return e
		}
	}
	for _, v := range bad {
		_, e = tx.ExecContext(ctx, `INSERT INTO billing_anomaly_orders(instance_id,source_log_id,job_id,created_at,request_id,upstream_request_id,user_id,username,token_id,token_name,channel_id,channel_name,model_name,group_name,prompt_tokens,completion_tokens,cache_tokens,cache_write_tokens,cache_write_5m_tokens,cache_write_1h_tokens,quota,max_context_tokens,input_price,output_price,cache_price,cache_write_price,input_amount,output_amount,cache_amount,cache_write_amount,reference_amount,actual_amount,reasons,detected_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE token_id=VALUES(token_id),token_name=VALUES(token_name),channel_id=VALUES(channel_id),channel_name=VALUES(channel_name),reasons=VALUES(reasons),max_context_tokens=VALUES(max_context_tokens),input_price=VALUES(input_price),output_price=VALUES(output_price),cache_price=VALUES(cache_price),cache_write_price=VALUES(cache_write_price),input_amount=VALUES(input_amount),output_amount=VALUES(output_amount),cache_amount=VALUES(cache_amount),cache_write_amount=VALUES(cache_write_amount),reference_amount=VALUES(reference_amount),actual_amount=VALUES(actual_amount),detected_at=VALUES(detected_at)`, v.InstanceID, v.SourceLogID, v.JobID, v.CreatedAt, v.RequestID, v.UpstreamRequestID, v.UserID, v.Username, v.TokenID, v.TokenName, v.ChannelID, v.ChannelName, v.ModelName, v.GroupName, v.PromptTokens, v.CompletionTokens, v.CacheTokens, v.CacheWriteTokens, v.CacheWrite5mTokens, v.CacheWrite1hTokens, v.Quota, v.MaxContextTokens, v.InputPrice, v.OutputPrice, v.CachePrice, v.CacheWritePrice, v.InputAmount, v.OutputAmount, v.CacheAmount, v.CacheWriteAmount, v.ReferenceAmount, v.ActualAmount, v.Reasons, v.DetectedAt)
		if e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, `UPDATE billing_job_steps SET cursor_created_at=?,cursor_id=?,processed_rows=processed_rows+?,abnormal_rows=abnormal_rows+?,updated_at=? WHERE job_id=? AND step_no=?`, c.CreatedUnix, c.ID, pageRows, len(bad), now, j.ID, st.StepNo)
	if e != nil {
		return e
	}
	return tx.Commit()
}

func (s Store) AppendBillingVerificationPage(ctx context.Context, j billing.Job, st billing.JobStep, items []billing.VerificationRow, c billing.LogCursor, pageRows int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	day := st.From.In(billing.BusinessLocation).Format("2006-01-02")
	for _, v := range items {
		_, err = tx.ExecContext(ctx, `INSERT INTO billing_verification_hourly(job_id,bill_day,user_id,username,model_name,group_name,source_rows,normal_rows,abnormal_rows,source_quota,normal_quota,abnormal_quota,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE username=VALUES(username),source_rows=source_rows+VALUES(source_rows),normal_rows=normal_rows+VALUES(normal_rows),abnormal_rows=abnormal_rows+VALUES(abnormal_rows),source_quota=source_quota+VALUES(source_quota),normal_quota=normal_quota+VALUES(normal_quota),abnormal_quota=abnormal_quota+VALUES(abnormal_quota),updated_at=VALUES(updated_at)`, j.ID, day, v.UserID, v.Username, v.ModelName, v.GroupName, v.SourceRows, v.NormalRows, v.AbnormalRows, v.SourceQuota, v.NormalQuota, v.AbnormalQuota, now)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE billing_job_steps SET cursor_created_at=?,cursor_id=?,processed_rows=processed_rows+?,updated_at=? WHERE job_id=? AND step_no=?`, c.CreatedUnix, c.ID, pageRows, now, j.ID, st.StepNo)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) FinalizeBillingVerification(ctx context.Context, j billing.Job, source billing.Job) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `DELETE FROM billing_verification_results WHERE job_id=?`, j.ID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO billing_verification_results(job_id,source_job_id,day,user_id,username,model_name,group_name,source_rows,verified_normal_rows,billed_normal_rows,verified_abnormal_rows,billed_abnormal_rows,source_quota,verified_normal_quota,billed_normal_quota,verified_abnormal_quota,billed_abnormal_quota,status,created_at)
WITH v AS (SELECT bill_day day,user_id,MAX(username) username,model_name,group_name,SUM(source_rows) source_rows,SUM(normal_rows) normal_rows,SUM(abnormal_rows) abnormal_rows,SUM(source_quota) source_quota,SUM(normal_quota) normal_quota,SUM(abnormal_quota) abnormal_quota FROM billing_verification_hourly WHERE job_id=? GROUP BY bill_day,user_id,model_name,group_name),
b AS (SELECT day,user_id,MAX(username) username,model_name,group_name,SUM(request_count) normal_rows,SUM(quota) normal_quota FROM billing_daily_versions WHERE job_id=? GROUP BY day,user_id,model_name,group_name),
a AS (SELECT DATE(CONVERT_TZ(created_at,'+00:00','+08:00')) day,user_id,MAX(username) username,model_name,group_name,COUNT(*) abnormal_rows,COALESCE(SUM(quota),0) abnormal_quota FROM billing_anomaly_orders WHERE job_id=? GROUP BY day,user_id,model_name,group_name),
k AS (SELECT day,user_id,model_name,group_name FROM v UNION SELECT day,user_id,model_name,group_name FROM b UNION SELECT day,user_id,model_name,group_name FROM a)
SELECT ?,?,k.day,k.user_id,COALESCE(v.username,b.username,a.username,''),k.model_name,k.group_name,COALESCE(v.source_rows,0),COALESCE(v.normal_rows,0),COALESCE(b.normal_rows,0),COALESCE(v.abnormal_rows,0),COALESCE(a.abnormal_rows,0),COALESCE(v.source_quota,0),COALESCE(v.normal_quota,0),COALESCE(b.normal_quota,0),COALESCE(v.abnormal_quota,0),COALESCE(a.abnormal_quota,0),IF(COALESCE(v.normal_rows,0)=COALESCE(b.normal_rows,0) AND COALESCE(v.normal_quota,0)=COALESCE(b.normal_quota,0) AND COALESCE(v.abnormal_rows,0)=COALESCE(a.abnormal_rows,0) AND COALESCE(v.abnormal_quota,0)=COALESCE(a.abnormal_quota,0) AND COALESCE(v.source_rows,0)=COALESCE(v.normal_rows,0)+COALESCE(v.abnormal_rows,0) AND COALESCE(v.source_quota,0)=COALESCE(v.normal_quota,0)+COALESCE(v.abnormal_quota,0),'matched','mismatch'),?
FROM k LEFT JOIN v USING(day,user_id,model_name,group_name) LEFT JOIN b USING(day,user_id,model_name,group_name) LEFT JOIN a USING(day,user_id,model_name,group_name)`, j.ID, source.ID, source.ID, j.ID, source.ID, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE billing_jobs SET status='complete',finished_at=?,updated_at=? WHERE id=? AND completed_steps>=total_steps`, now, now, j.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) BillingVerificationResults(ctx context.Context, jobID string, mismatchesOnly bool, limit, offset int) ([]billing.VerificationResult, billing.VerificationSummary, int, error) {
	where := ` WHERE job_id=?`
	if mismatchesOnly {
		where += ` AND status='mismatch'`
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_verification_results`+where, jobID).Scan(&total); err != nil {
		return nil, billing.VerificationSummary{}, 0, err
	}
	q := `SELECT day,user_id,username,model_name,group_name,source_rows,verified_normal_rows,billed_normal_rows,verified_abnormal_rows,billed_abnormal_rows,source_quota,verified_normal_quota,billed_normal_quota,verified_abnormal_quota,billed_abnormal_quota,status FROM billing_verification_results` + where + ` ORDER BY day,user_id,model_name,group_name LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(ctx, q, jobID, limit, offset)
	if err != nil {
		return nil, billing.VerificationSummary{}, 0, err
	}
	defer rows.Close()
	items := []billing.VerificationResult{}
	for rows.Next() {
		var v billing.VerificationResult
		if err = rows.Scan(&v.Day, &v.UserID, &v.Username, &v.ModelName, &v.GroupName, &v.SourceRows, &v.VerifiedNormalRows, &v.BilledNormalRows, &v.VerifiedAbnormalRows, &v.BilledAbnormalRows, &v.SourceQuota, &v.VerifiedNormalQuota, &v.BilledNormalQuota, &v.VerifiedAbnormalQuota, &v.BilledAbnormalQuota, &v.Status); err != nil {
			return nil, billing.VerificationSummary{}, 0, err
		}
		items = append(items, v)
	}
	var summary billing.VerificationSummary
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(source_rows),0),COALESCE(SUM(verified_normal_rows),0),COALESCE(SUM(billed_normal_rows),0),COALESCE(SUM(verified_abnormal_rows),0),COALESCE(SUM(billed_abnormal_rows),0),COALESCE(SUM(source_quota),0),COALESCE(SUM(verified_normal_quota),0),COALESCE(SUM(billed_normal_quota),0),COALESCE(SUM(verified_abnormal_quota),0),COALESCE(SUM(billed_abnormal_quota),0),COALESCE(SUM(status='matched'),0),COALESCE(SUM(status='mismatch'),0) FROM billing_verification_results WHERE job_id=?`, jobID).Scan(&summary.SourceRows, &summary.VerifiedNormalRows, &summary.BilledNormalRows, &summary.VerifiedAbnormalRows, &summary.BilledAbnormalRows, &summary.SourceQuota, &summary.VerifiedNormalQuota, &summary.BilledNormalQuota, &summary.VerifiedAbnormalQuota, &summary.BilledAbnormalQuota, &summary.MatchedRows, &summary.MismatchedRows)
	return items, summary, total, err
}
func (s Store) CompleteBillingStep(ctx context.Context, j billing.Job, st billing.JobStep, processed, bad int64) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	res, e := tx.ExecContext(ctx, `UPDATE billing_job_steps SET status='complete',finished_at=?,updated_at=? WHERE job_id=? AND step_no=? AND status='running'`, now, now, j.ID, st.StepNo)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		_, e = tx.ExecContext(ctx, `UPDATE billing_jobs SET completed_steps=completed_steps+1,abnormal_rows=abnormal_rows+?,updated_at=? WHERE id=? AND status IN ('pending','running')`, bad, now, j.ID)
	}
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s Store) FailBillingStep(ctx context.Context, j billing.Job, st billing.JobStep, cause error) error {
	now := time.Now().UTC()
	_, e := s.db.ExecContext(ctx, `UPDATE billing_job_steps s JOIN billing_jobs j ON j.id=s.job_id SET s.status=IF(s.attempts<3,'pending','failed'),s.error_message=?,s.updated_at=?,j.status=IF(s.attempts<3,'running','failed'),j.error_message=?,j.updated_at=? WHERE s.job_id=? AND s.step_no=?`, cause.Error(), now, cause.Error(), now, j.ID, st.StepNo)
	return e
}

func (s Store) FinalizeBillingJob(ctx context.Context, j billing.Job) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	var status string
	if e = tx.QueryRowContext(ctx, `SELECT status FROM billing_jobs WHERE id=? FOR UPDATE`, j.ID).Scan(&status); e != nil {
		return e
	}
	if status != "pending" && status != "running" {
		return nil
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO billing_daily_versions(job_id,instance_id,user_id,username,model_name,group_name,tier_from,day,request_count,prompt_tokens,completion_tokens,cache_tokens,cache_write_tokens,cache_write_5m_tokens,cache_write_1h_tokens,quota,updated_at) SELECT ?,instance_id,user_id,MAX(username),model_name,group_name,tier_from,DATE(CONVERT_TZ(hour_start,'+00:00','+08:00')),SUM(request_count),SUM(prompt_tokens),SUM(completion_tokens),SUM(cache_tokens),SUM(cache_write_tokens),SUM(cache_write_5m_tokens),SUM(cache_write_1h_tokens),SUM(quota),? FROM billing_hourly WHERE job_id=? GROUP BY instance_id,user_id,model_name,group_name,tier_from,DATE(CONVERT_TZ(hour_start,'+00:00','+08:00'))`, j.ID, now, j.ID)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO billing_channel_daily_versions(job_id,instance_id,channel_id,channel_name,model_name,group_name,tier_from,day,request_count,prompt_tokens,completion_tokens,cache_tokens,cache_write_tokens,cache_write_5m_tokens,cache_write_1h_tokens,quota,updated_at) SELECT ?,instance_id,channel_id,MAX(channel_name),model_name,group_name,tier_from,DATE(CONVERT_TZ(hour_start,'+00:00','+08:00')),SUM(request_count),SUM(prompt_tokens),SUM(completion_tokens),SUM(cache_tokens),SUM(cache_write_tokens),SUM(cache_write_5m_tokens),SUM(cache_write_1h_tokens),SUM(quota),? FROM billing_channel_hourly WHERE job_id=? GROUP BY instance_id,channel_id,model_name,group_name,tier_from,DATE(CONVERT_TZ(hour_start,'+00:00','+08:00'))`, j.ID, now, j.ID)
	if e != nil {
		return e
	}
	localFrom, localTo := j.From.In(billing.BusinessLocation), j.To.In(billing.BusinessLocation)
	for d := dateAt(localFrom); d.Before(localTo); d = d.AddDate(0, 0, 1) {
		if _, e = tx.ExecContext(ctx, `INSERT INTO billing_active_versions(instance_id,day,job_id,activated_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE job_id=VALUES(job_id),activated_at=VALUES(activated_at)`, j.InstanceID, d, j.ID, now); e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, `UPDATE billing_jobs SET status='complete',finished_at=?,updated_at=? WHERE id=? AND completed_steps>=total_steps`, now, now, j.ID)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func dateAt(v time.Time) time.Time {
	y, m, d := v.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, v.Location())
}

func (s Store) QueryBillingAnomalies(ctx context.Context, site, jobID string, userID, channelID int64, from, to, cursorTime time.Time, cursorID int64, limit int) ([]billing.AnomalyOrder, error) {
	q := `SELECT instance_id,source_log_id,job_id,created_at,request_id,upstream_request_id,user_id,username,token_id,token_name,channel_id,channel_name,model_name,group_name,prompt_tokens,completion_tokens,cache_tokens,cache_write_tokens,cache_write_5m_tokens,cache_write_1h_tokens,quota,max_context_tokens,input_price,output_price,cache_price,cache_write_price,input_amount,output_amount,cache_amount,cache_write_amount,reference_amount,actual_amount,reasons,detected_at FROM billing_anomaly_orders WHERE instance_id=? AND job_id=? AND created_at>=? AND created_at<? AND (created_at>? OR (created_at=? AND source_log_id>?))`
	args := []any{site, jobID, from, to, cursorTime, cursorTime, cursorID}
	if userID > 0 {
		q += ` AND user_id=?`
		args = append(args, userID)
	}
	if channelID > 0 {
		q += ` AND channel_id=?`
		args = append(args, channelID)
	}
	q += ` ORDER BY created_at,source_log_id LIMIT ?`
	args = append(args, limit)
	rows, e := s.db.QueryContext(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []billing.AnomalyOrder{}
	for rows.Next() {
		var v billing.AnomalyOrder
		if e = rows.Scan(&v.InstanceID, &v.SourceLogID, &v.JobID, &v.CreatedAt, &v.RequestID, &v.UpstreamRequestID, &v.UserID, &v.Username, &v.TokenID, &v.TokenName, &v.ChannelID, &v.ChannelName, &v.ModelName, &v.GroupName, &v.PromptTokens, &v.CompletionTokens, &v.CacheTokens, &v.CacheWriteTokens, &v.CacheWrite5mTokens, &v.CacheWrite1hTokens, &v.Quota, &v.MaxContextTokens, &v.InputPrice, &v.OutputPrice, &v.CachePrice, &v.CacheWritePrice, &v.InputAmount, &v.OutputAmount, &v.CacheAmount, &v.CacheWriteAmount, &v.ReferenceAmount, &v.ActualAmount, &v.Reasons, &v.DetectedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s Store) BillingTokenAnomalyCountsForJob(ctx context.Context, jobID string, userID, tokenID int64, from, to time.Time) ([]billing.AnomalyCount, error) {
	query := `SELECT user_id,token_id,MAX(token_name),DATE(CONVERT_TZ(created_at,'+00:00','+08:00')),model_name,group_name,COUNT(*),COALESCE(SUM(actual_amount),0) FROM billing_anomaly_orders WHERE job_id=? AND user_id=? AND created_at>=? AND created_at<?`
	args := []any{jobID, userID, from.UTC(), to.UTC()}
	if tokenID >= 0 {
		query += ` AND token_id=?`
		args = append(args, tokenID)
	}
	query += ` GROUP BY user_id,token_id,DATE(CONVERT_TZ(created_at,'+00:00','+08:00')),model_name,group_name`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []billing.AnomalyCount
	for rows.Next() {
		var item billing.AnomalyCount
		if err = rows.Scan(&item.UserID, &item.TokenID, &item.TokenName, &item.Day, &item.ModelName, &item.GroupName, &item.Count, &item.Amount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) BillingAnomalyCountsForJob(ctx context.Context, jobID string) ([]billing.AnomalyCount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id,channel_id,DATE(CONVERT_TZ(created_at,'+00:00','+08:00')),model_name,group_name,COUNT(*),COALESCE(SUM(actual_amount),0) FROM billing_anomaly_orders WHERE job_id=? GROUP BY user_id,channel_id,DATE(CONVERT_TZ(created_at,'+00:00','+08:00')),model_name,group_name`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.AnomalyCount{}
	for rows.Next() {
		var item billing.AnomalyCount
		if err = rows.Scan(&item.UserID, &item.ChannelID, &item.Day, &item.ModelName, &item.GroupName, &item.Count, &item.Amount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) BillingAnomalyCountsForJobRange(ctx context.Context, jobID string, from, to time.Time) ([]billing.AnomalyCount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id,channel_id,DATE(CONVERT_TZ(created_at,'+00:00','+08:00')),model_name,group_name,COUNT(*),COALESCE(SUM(actual_amount),0) FROM billing_anomaly_orders WHERE job_id=? AND created_at>=? AND created_at<? GROUP BY user_id,channel_id,DATE(CONVERT_TZ(created_at,'+00:00','+08:00')),model_name,group_name`, jobID, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []billing.AnomalyCount
	for rows.Next() {
		var item billing.AnomalyCount
		if err = rows.Scan(&item.UserID, &item.ChannelID, &item.Day, &item.ModelName, &item.GroupName, &item.Count, &item.Amount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) UpdateBillingAnomalyActualAmounts(ctx context.Context, jobID, quotaPerUnit string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE billing_anomaly_orders SET actual_amount=ROUND(GREATEST(quota,0)/?,6) WHERE job_id=?`, quotaPerUnit, jobID)
	return err
}
