package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"time"
)

func (s Store) CreateBillingJob(ctx context.Context, j billing.Job, steps []billing.JobStep) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(ctx, `INSERT INTO billing_jobs(id,request_key,instance_id,job_type,user_id,range_from,range_to,status,total_steps,requested_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, j.ID, nullBillingRequestKey(j.RequestKey), j.InstanceID, j.JobType, j.UserID, j.From, j.To, j.Status, j.TotalSteps, j.RequestedBy, j.CreatedAt, j.UpdatedAt)
	if e != nil {
		return e
	}
	for _, v := range steps {
		if _, e = tx.ExecContext(ctx, `INSERT INTO billing_job_steps(job_id,step_no,range_from,range_to,status,updated_at) VALUES(?,?,?,?,?,?)`, v.JobID, v.StepNo, v.From, v.To, "pending", j.UpdatedAt); e != nil {
			return e
		}
	}
	return tx.Commit()
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
func (s Store) PutBillingUserSetting(ctx context.Context, v billing.UserSetting) error {
	_, e := s.db.ExecContext(ctx, `INSERT INTO billing_user_settings(instance_id,user_id,use_tiered_pricing,updated_at,updated_by) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE use_tiered_pricing=VALUES(use_tiered_pricing),updated_at=VALUES(updated_at),updated_by=VALUES(updated_by)`, v.InstanceID, v.UserID, v.UseTieredPricing, v.UpdatedAt, v.UpdatedBy)
	return e
}

func (s Store) AppendBillingHour(ctx context.Context, j billing.Job, st billing.JobStep, items []billing.DailyRow, channels []billing.ChannelDailyRow, bad []billing.AnomalyOrder, c billing.LogCursor, pageRows int64) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	for _, v := range items {
		_, e = tx.ExecContext(ctx, `INSERT INTO billing_hourly(job_id,instance_id,hour_start,user_id,username,model_name,group_name,tier_from,request_count,prompt_tokens,completion_tokens,cache_tokens,quota,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE username=VALUES(username),request_count=request_count+VALUES(request_count),prompt_tokens=prompt_tokens+VALUES(prompt_tokens),completion_tokens=completion_tokens+VALUES(completion_tokens),cache_tokens=cache_tokens+VALUES(cache_tokens),quota=quota+VALUES(quota),updated_at=VALUES(updated_at)`, j.ID, j.InstanceID, st.From.UTC(), v.UserID, v.Username, v.ModelName, v.GroupName, v.TierFrom, v.RequestCount, v.PromptTokens, v.CompletionTokens, v.CacheTokens, v.Quota, now)
		if e != nil {
			return e
		}
	}
	for _, v := range channels {
		_, e = tx.ExecContext(ctx, `INSERT INTO billing_channel_hourly(job_id,instance_id,hour_start,channel_id,channel_name,model_name,group_name,tier_from,request_count,prompt_tokens,completion_tokens,cache_tokens,quota,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE channel_name=VALUES(channel_name),request_count=request_count+VALUES(request_count),prompt_tokens=prompt_tokens+VALUES(prompt_tokens),completion_tokens=completion_tokens+VALUES(completion_tokens),cache_tokens=cache_tokens+VALUES(cache_tokens),quota=quota+VALUES(quota),updated_at=VALUES(updated_at)`, j.ID, j.InstanceID, st.From.UTC(), v.ChannelID, v.ChannelName, v.ModelName, v.GroupName, v.TierFrom, v.RequestCount, v.PromptTokens, v.CompletionTokens, v.CacheTokens, v.Quota, now)
		if e != nil {
			return e
		}
	}
	for _, v := range bad {
		_, e = tx.ExecContext(ctx, `INSERT INTO billing_anomaly_orders(instance_id,source_log_id,job_id,created_at,request_id,upstream_request_id,user_id,username,channel_id,channel_name,model_name,group_name,prompt_tokens,completion_tokens,cache_tokens,quota,max_context_tokens,input_price,output_price,cache_price,input_amount,output_amount,cache_amount,reference_amount,reasons,detected_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE job_id=VALUES(job_id),channel_id=VALUES(channel_id),channel_name=VALUES(channel_name),reasons=VALUES(reasons),max_context_tokens=VALUES(max_context_tokens),input_price=VALUES(input_price),output_price=VALUES(output_price),cache_price=VALUES(cache_price),input_amount=VALUES(input_amount),output_amount=VALUES(output_amount),cache_amount=VALUES(cache_amount),reference_amount=VALUES(reference_amount),detected_at=VALUES(detected_at)`, v.InstanceID, v.SourceLogID, v.JobID, v.CreatedAt, v.RequestID, v.UpstreamRequestID, v.UserID, v.Username, v.ChannelID, v.ChannelName, v.ModelName, v.GroupName, v.PromptTokens, v.CompletionTokens, v.CacheTokens, v.Quota, v.MaxContextTokens, v.InputPrice, v.OutputPrice, v.CachePrice, v.InputAmount, v.OutputAmount, v.CacheAmount, v.ReferenceAmount, v.Reasons, v.DetectedAt)
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
func (s Store) CompleteBillingStep(ctx context.Context, j billing.Job, st billing.JobStep, processed, bad int64) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	res, e := tx.ExecContext(ctx, `UPDATE billing_job_steps SET status='complete',finished_at=?,updated_at=? WHERE job_id=? AND step_no=? AND status<>'complete'`, now, now, j.ID, st.StepNo)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		_, e = tx.ExecContext(ctx, `UPDATE billing_jobs SET completed_steps=completed_steps+1,abnormal_rows=abnormal_rows+?,updated_at=? WHERE id=?`, bad, now, j.ID)
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
	_, e = tx.ExecContext(ctx, `INSERT INTO billing_daily_versions(job_id,instance_id,user_id,username,model_name,group_name,tier_from,day,request_count,prompt_tokens,completion_tokens,cache_tokens,quota,updated_at) SELECT ?,instance_id,user_id,MAX(username),model_name,group_name,tier_from,DATE(CONVERT_TZ(hour_start,'+00:00','+08:00')),SUM(request_count),SUM(prompt_tokens),SUM(completion_tokens),SUM(cache_tokens),SUM(quota),? FROM billing_hourly WHERE job_id=? GROUP BY instance_id,user_id,model_name,group_name,tier_from,DATE(CONVERT_TZ(hour_start,'+00:00','+08:00'))`, j.ID, now, j.ID)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO billing_channel_daily_versions(job_id,instance_id,channel_id,channel_name,model_name,group_name,tier_from,day,request_count,prompt_tokens,completion_tokens,cache_tokens,quota,updated_at) SELECT ?,instance_id,channel_id,MAX(channel_name),model_name,group_name,tier_from,DATE(CONVERT_TZ(hour_start,'+00:00','+08:00')),SUM(request_count),SUM(prompt_tokens),SUM(completion_tokens),SUM(cache_tokens),SUM(quota),? FROM billing_channel_hourly WHERE job_id=? GROUP BY instance_id,channel_id,model_name,group_name,tier_from,DATE(CONVERT_TZ(hour_start,'+00:00','+08:00'))`, j.ID, now, j.ID)
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

func (s Store) QueryBillingAnomalies(ctx context.Context, site string, userID, channelID int64, from, to, cursorTime time.Time, cursorID int64, limit int) ([]billing.AnomalyOrder, error) {
	q := `SELECT instance_id,source_log_id,job_id,created_at,request_id,upstream_request_id,user_id,username,channel_id,channel_name,model_name,group_name,prompt_tokens,completion_tokens,cache_tokens,quota,max_context_tokens,input_price,output_price,cache_price,input_amount,output_amount,cache_amount,reference_amount,reasons,detected_at FROM billing_anomaly_orders WHERE instance_id=? AND created_at>=? AND created_at<? AND (created_at>? OR (created_at=? AND source_log_id>?))`
	args := []any{site, from, to, cursorTime, cursorTime, cursorID}
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
		if e = rows.Scan(&v.InstanceID, &v.SourceLogID, &v.JobID, &v.CreatedAt, &v.RequestID, &v.UpstreamRequestID, &v.UserID, &v.Username, &v.ChannelID, &v.ChannelName, &v.ModelName, &v.GroupName, &v.PromptTokens, &v.CompletionTokens, &v.CacheTokens, &v.Quota, &v.MaxContextTokens, &v.InputPrice, &v.OutputPrice, &v.CachePrice, &v.InputAmount, &v.OutputAmount, &v.CacheAmount, &v.ReferenceAmount, &v.Reasons, &v.DetectedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
