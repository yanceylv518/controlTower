package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"fmt"
	"math/big"
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
	err := s.db.QueryRowContext(ctx, `SELECT id,instance_id,job_type,user_id,range_from,range_to,status,total_steps,completed_steps,abnormal_rows,error_message,output_path,requested_by,created_at,updated_at FROM billing_jobs WHERE status IN ('pending','running','publishing') ORDER BY created_at LIMIT 1`).Scan(&j.ID, &j.InstanceID, &j.JobType, &j.UserID, &j.From, &j.To, &j.Status, &j.TotalSteps, &j.CompletedSteps, &j.AbnormalRows, &j.ErrorMessage, &j.OutputPath, &j.RequestedBy, &j.CreatedAt, &j.UpdatedAt)
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
	result, err := tx.ExecContext(ctx, `UPDATE billing_jobs SET status='failed',error_message='cancelled manually',finished_at=?,updated_at=? WHERE id=? AND status IN ('pending','running','publishing')`, now, now, id)
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

// DeleteFailedBillingJob removes a failed task and all of its database-only
// intermediate results. Inactive generated files remain registered so the
// regular file-retention worker can remove them safely from disk.
func (s Store) DeleteFailedBillingJob(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM billing_jobs WHERE id=? FOR UPDATE`, id).Scan(&status); err != nil {
		return err
	}
	if status != "failed" {
		return fmt.Errorf("billing job is not failed")
	}
	for _, table := range []string{
		"billing_verification_results", "billing_verification_hourly", "billing_verification_jobs",
		"billing_request_details", "billing_compact_daily_totals", "billing_anomaly_orders", "billing_token_daily_versions",
		"billing_channel_daily_versions", "billing_channel_hourly", "billing_daily_versions",
		"billing_hourly", "billing_job_user_settings", "billing_job_steps",
	} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE job_id=?`, id); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM billing_jobs WHERE id=? AND status='failed'`, id)
	if err != nil {
		return err
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
		return fmt.Errorf("failed billing job was not deleted")
	}
	return tx.Commit()
}

func (s Store) ListBillingJobs(ctx context.Context, instanceID, status string, limit int) ([]billing.Job, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := `SELECT jobs.id,jobs.instance_id,jobs.job_type,jobs.user_id,jobs.range_from,jobs.range_to,jobs.status,jobs.total_steps,jobs.completed_steps,jobs.abnormal_rows,(SELECT COALESCE(SUM(details.request_count),0) FROM billing_compact_daily_totals details WHERE details.job_id=jobs.id),(SELECT COUNT(DISTINCT files.bill_day) FROM billing_user_daily_files files WHERE files.job_id=jobs.id),(SELECT COALESCE(DATE_FORMAT(MAX(files.bill_day),'%Y-%m-%d'),'') FROM billing_user_daily_files files WHERE files.job_id=jobs.id),jobs.error_message,jobs.output_path,jobs.requested_by,jobs.created_at,jobs.updated_at FROM billing_jobs jobs`
	args := []any{}
	conditions := []string{}
	if instanceID != "" {
		conditions = append(conditions, `jobs.instance_id=?`)
		args = append(args, instanceID)
	}
	if status != "" {
		conditions = append(conditions, `jobs.status=?`)
		args = append(args, status)
	}
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	query += ` ORDER BY FIELD(jobs.status,'publishing','running','pending','failed','complete'),jobs.created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.Job{}
	for rows.Next() {
		var j billing.Job
		if err = rows.Scan(&j.ID, &j.InstanceID, &j.JobType, &j.UserID, &j.From, &j.To, &j.Status, &j.TotalSteps, &j.CompletedSteps, &j.AbnormalRows, &j.BilledRows, &j.OutputDays, &j.OutputLatestDay, &j.ErrorMessage, &j.OutputPath, &j.RequestedBy, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		if err = s.enrichBillingStatement(ctx, &j); err != nil {
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

func (s Store) ClaimBillingPublish(ctx context.Context) (billing.Job, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return billing.Job{}, false, err
	}
	defer tx.Rollback()
	var j billing.Job
	err = tx.QueryRowContext(ctx, `SELECT id,instance_id,job_type,user_id,range_from,range_to,status,total_steps,completed_steps,abnormal_rows,error_message,output_path,requested_by,created_at,updated_at FROM billing_jobs WHERE job_type IN ('generate','user_statement','upstream_statement') AND status IN ('running','publishing') AND completed_steps>=total_steps ORDER BY created_at LIMIT 1 FOR UPDATE`).Scan(&j.ID, &j.InstanceID, &j.JobType, &j.UserID, &j.From, &j.To, &j.Status, &j.TotalSteps, &j.CompletedSteps, &j.AbnormalRows, &j.ErrorMessage, &j.OutputPath, &j.RequestedBy, &j.CreatedAt, &j.UpdatedAt)
	if err == sql.ErrNoRows {
		return billing.Job{}, false, nil
	}
	if err != nil {
		return billing.Job{}, false, err
	}
	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `UPDATE billing_jobs SET status='publishing',publish_attempts=publish_attempts+1,updated_at=? WHERE id=?`, now, j.ID); err != nil {
		return billing.Job{}, false, err
	}
	j.Status = "publishing"
	j.UpdatedAt = now
	return j, true, tx.Commit()
}

func (s Store) FailBillingPublish(ctx context.Context, j billing.Job, cause error) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `UPDATE billing_jobs SET status=IF(publish_attempts<3,'running','failed'),error_message=?,finished_at=IF(publish_attempts<3,NULL,?),updated_at=? WHERE id=? AND status='publishing'`, cause.Error(), now, now, j.ID)
	return err
}

func (s Store) BillingJob(ctx context.Context, id string) (billing.Job, error) {
	var j billing.Job
	e := s.db.QueryRowContext(ctx, `SELECT id,instance_id,job_type,user_id,range_from,range_to,status,total_steps,completed_steps,abnormal_rows,error_message,output_path,requested_by,created_at,updated_at FROM billing_jobs WHERE id=?`, id).Scan(&j.ID, &j.InstanceID, &j.JobType, &j.UserID, &j.From, &j.To, &j.Status, &j.TotalSteps, &j.CompletedSteps, &j.AbnormalRows, &j.ErrorMessage, &j.OutputPath, &j.RequestedBy, &j.CreatedAt, &j.UpdatedAt)
	if e == nil {
		e = s.enrichBillingStatement(ctx, &j)
	}
	return j, e
}

func (s Store) ListBillingJobSteps(ctx context.Context, jobID string) ([]billing.JobStep, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT job_id,step_no,range_from,range_to,status,processed_rows,abnormal_rows,attempts,error_message,cursor_created_at,cursor_id FROM billing_job_steps WHERE job_id=? ORDER BY step_no`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	steps := []billing.JobStep{}
	for rows.Next() {
		var step billing.JobStep
		if err = rows.Scan(&step.JobID, &step.StepNo, &step.From, &step.To, &step.Status, &step.ProcessedRows, &step.AbnormalRows, &step.Attempts, &step.ErrorMessage, &step.Cursor.CreatedUnix, &step.Cursor.ID); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, rows.Err()
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

func (s Store) AppendBillingHour(ctx context.Context, j billing.Job, st billing.JobStep, items []billing.DailyRow, tokens []billing.TokenDailyRow, channels []billing.ChannelDailyRow, details []billing.RequestDetail, bad []billing.AnomalyOrder, mismatches []billing.ReconciliationOrder, c billing.LogCursor, pageRows int64) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	// The compact daily table supersedes the three legacy hourly/token/channel
	// projections. Keeping them out of the hot path reduces each source page to
	// one normal aggregate statement plus (usually empty) anomaly batches.
	_, _, _ = items, tokens, channels
	decimalValue := func(value string) string {
		if strings.TrimSpace(value) == "" {
			return "0"
		}
		return value
	}
	type compactKey struct {
		day                        string
		userID, tokenID, channelID int64
		model                      string
	}
	type compactValue struct {
		username, tokenName, channelName string
		requests, prompt, completion     int64
		cacheRead, cacheWrite            int64
		write5m, write1h, quota          int64
		amount                           *big.Rat
	}
	compact := map[compactKey]compactValue{}
	for _, v := range details {
		key := compactKey{v.BillDay.In(billing.BusinessLocation).Format("2006-01-02"), v.UserID, v.TokenID, v.ChannelID, v.ModelName}
		value := compact[key]
		value.username, value.tokenName, value.channelName = v.Username, v.TokenName, v.ChannelName
		value.requests++
		value.prompt += v.PromptTokens
		value.completion += v.CompletionTokens
		value.cacheRead += v.CacheReadTokens
		value.cacheWrite += v.CacheWriteTokens
		value.write5m += v.CacheWrite5mTokens
		value.write1h += v.CacheWrite1hTokens
		value.quota += v.CalculatedQuota
		if value.amount == nil {
			value.amount = new(big.Rat)
		}
		if amount, ok := new(big.Rat).SetString(decimalValue(v.Charge.Total)); ok {
			value.amount.Add(value.amount, amount)
		}
		compact[key] = value
	}
	const compactColumns = 20
	compactPlaceholder := "(" + strings.TrimRight(strings.Repeat("?,", compactColumns), ",") + ")"
	values, args := make([]string, 0, len(compact)), make([]any, 0, len(compact)*compactColumns)
	for key, value := range compact {
		values = append(values, compactPlaceholder)
		args = append(args, j.ID, j.InstanceID, key.day, key.userID, value.username, key.tokenID, value.tokenName, key.channelID, value.channelName, key.model, value.requests, value.prompt, value.completion, value.cacheRead, value.cacheWrite, value.write5m, value.write1h, value.quota, value.amount.FloatString(12), now)
	}
	if len(values) > 0 {
		query := `INSERT INTO billing_compact_daily_totals(job_id,instance_id,bill_day,user_id,username,token_id,token_name,channel_id,channel_name,model_name,request_count,prompt_tokens,completion_tokens,cache_read_tokens,cache_write_tokens,cache_write_5m_tokens,cache_write_1h_tokens,calculated_quota,total_amount,updated_at) VALUES ` + strings.Join(values, ",") + ` ON DUPLICATE KEY UPDATE username=VALUES(username),token_name=VALUES(token_name),channel_name=VALUES(channel_name),request_count=request_count+VALUES(request_count),prompt_tokens=prompt_tokens+VALUES(prompt_tokens),completion_tokens=completion_tokens+VALUES(completion_tokens),cache_read_tokens=cache_read_tokens+VALUES(cache_read_tokens),cache_write_tokens=cache_write_tokens+VALUES(cache_write_tokens),cache_write_5m_tokens=cache_write_5m_tokens+VALUES(cache_write_5m_tokens),cache_write_1h_tokens=cache_write_1h_tokens+VALUES(cache_write_1h_tokens),calculated_quota=calculated_quota+VALUES(calculated_quota),total_amount=total_amount+VALUES(total_amount),updated_at=VALUES(updated_at)`
		if _, e = tx.ExecContext(ctx, query, args...); e != nil {
			return e
		}
	}
	const anomalyColumns = 34
	const anomalyBatchSize = 250
	anomalyPlaceholder := "(" + strings.TrimRight(strings.Repeat("?,", anomalyColumns), ",") + ")"
	for offset := 0; offset < len(bad); offset += anomalyBatchSize {
		end := offset + anomalyBatchSize
		if end > len(bad) {
			end = len(bad)
		}
		values, args := make([]string, 0, end-offset), make([]any, 0, (end-offset)*anomalyColumns)
		for _, v := range bad[offset:end] {
			values = append(values, anomalyPlaceholder)
			args = append(args, v.InstanceID, v.SourceLogID, v.JobID, v.CreatedAt, v.RequestID, v.UpstreamRequestID, v.UserID, v.Username, v.TokenID, v.TokenName, v.ChannelID, v.ChannelName, v.ModelName, v.GroupName, v.PromptTokens, v.CompletionTokens, v.CacheTokens, v.CacheWriteTokens, v.CacheWrite5mTokens, v.CacheWrite1hTokens, v.Quota, v.MaxContextTokens, v.InputPrice, v.OutputPrice, v.CachePrice, v.CacheWritePrice, v.InputAmount, v.OutputAmount, v.CacheAmount, v.CacheWriteAmount, v.ReferenceAmount, v.ActualAmount, v.Reasons, v.DetectedAt)
		}
		query := `INSERT INTO billing_anomaly_orders(instance_id,source_log_id,job_id,created_at,request_id,upstream_request_id,user_id,username,token_id,token_name,channel_id,channel_name,model_name,group_name,prompt_tokens,completion_tokens,cache_tokens,cache_write_tokens,cache_write_5m_tokens,cache_write_1h_tokens,quota,max_context_tokens,input_price,output_price,cache_price,cache_write_price,input_amount,output_amount,cache_amount,cache_write_amount,reference_amount,actual_amount,reasons,detected_at) VALUES ` + strings.Join(values, ",") + ` ON DUPLICATE KEY UPDATE token_id=VALUES(token_id),token_name=VALUES(token_name),channel_id=VALUES(channel_id),channel_name=VALUES(channel_name),reasons=VALUES(reasons),max_context_tokens=VALUES(max_context_tokens),input_price=VALUES(input_price),output_price=VALUES(output_price),cache_price=VALUES(cache_price),cache_write_price=VALUES(cache_write_price),input_amount=VALUES(input_amount),output_amount=VALUES(output_amount),cache_amount=VALUES(cache_amount),cache_write_amount=VALUES(cache_write_amount),reference_amount=VALUES(reference_amount),actual_amount=VALUES(actual_amount),detected_at=VALUES(detected_at)`
		if _, e = tx.ExecContext(ctx, query, args...); e != nil {
			return e
		}
	}
	const mismatchColumns = 25
	const mismatchBatchSize = 300
	mismatchPlaceholder := "(" + strings.TrimRight(strings.Repeat("?,", mismatchColumns), ",") + ")"
	for offset := 0; offset < len(mismatches); offset += mismatchBatchSize {
		end := offset + mismatchBatchSize
		if end > len(mismatches) {
			end = len(mismatches)
		}
		values, args := make([]string, 0, end-offset), make([]any, 0, (end-offset)*mismatchColumns)
		for _, v := range mismatches[offset:end] {
			values = append(values, mismatchPlaceholder)
			args = append(args, v.JobID, v.SourceLogID, v.InstanceID, time.Unix(v.CreatedUnix, 0), v.RequestID, v.UpstreamRequestID, v.UserID, v.Username, v.TokenID, v.TokenName, v.ChannelID, v.ChannelName, v.ModelName, v.PromptTokens, v.CompletionTokens, v.CacheReadTokens, v.CacheWriteTokens, v.CalculatedQuota, v.LoggedQuota, v.QuotaDifference, v.CalculatedAmount, v.LoggedAmount, v.AmountDifference, v.Reason, v.DetectedAt)
		}
		query := `INSERT INTO billing_statement_reconciliation_orders(job_id,source_log_id,instance_id,created_at,request_id,upstream_request_id,user_id,username,token_id,token_name,channel_id,channel_name,model_name,prompt_tokens,completion_tokens,cache_read_tokens,cache_write_tokens,calculated_quota,logged_quota,quota_difference,calculated_amount,logged_amount,amount_difference,reason,detected_at) VALUES ` + strings.Join(values, ",") + ` ON DUPLICATE KEY UPDATE calculated_quota=VALUES(calculated_quota),logged_quota=VALUES(logged_quota),quota_difference=VALUES(quota_difference),calculated_amount=VALUES(calculated_amount),logged_amount=VALUES(logged_amount),amount_difference=VALUES(amount_difference),reason=VALUES(reason),detected_at=VALUES(detected_at)`
		if _, e = tx.ExecContext(ctx, query, args...); e != nil {
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
b AS (SELECT bill_day day,user_id,MAX(username) username,model_name,'' group_name,SUM(request_count) normal_rows,SUM(calculated_quota) normal_quota FROM billing_compact_daily_totals WHERE job_id=? GROUP BY bill_day,user_id,model_name),
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
	if status != "pending" && status != "running" && status != "publishing" {
		return nil
	}
	if j.JobType == "user_statement" || j.JobType == "upstream_statement" {
		if _, e = tx.ExecContext(ctx, `DELETE FROM billing_request_details WHERE job_id=?`, j.ID); e != nil {
			return e
		}
		if e = s.finalizeBillingStatement(ctx, tx, j, now); e != nil {
			return e
		}
		return tx.Commit()
	}
	localFrom, localTo := j.From.In(billing.BusinessLocation), j.To.In(billing.BusinessLocation)
	for d := dateAt(localFrom); d.Before(localTo); d = d.AddDate(0, 0, 1) {
		// bill_day is a MySQL DATE. Passing a time.Time at Shanghai midnight lets
		// the driver normalize it to the previous UTC date/time, so equality can
		// silently match no rows. Always bind the calendar date as YYYY-MM-DD.
		billDay := d.Format("2006-01-02")
		// A non-forced range job may omit days already covered by an active bill.
		// Only activate dates that were actually scheduled for this job, otherwise
		// a partial regeneration could replace an existing active version with an
		// empty/incomplete job.
		var scheduledSteps int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_job_steps WHERE job_id=? AND DATE(CONVERT_TZ(range_from,'+00:00','+08:00'))=?`, j.ID, billDay).Scan(&scheduledSteps); e != nil {
			return e
		}
		if scheduledSteps == 0 {
			continue
		}
		if j.UserID == 0 {
			if _, e = tx.ExecContext(ctx, `INSERT INTO billing_active_versions(instance_id,day,job_id,activated_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE job_id=VALUES(job_id),activated_at=VALUES(activated_at)`, j.InstanceID, billDay, j.ID, now); e != nil {
				return e
			}
		}
		var normalRequests, anomalyRequests int64
		if e = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(request_count),0) FROM billing_compact_daily_totals WHERE job_id=? AND bill_day=?`, j.ID, billDay).Scan(&normalRequests); e != nil {
			return e
		}
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_anomaly_orders WHERE job_id=? AND DATE(CONVERT_TZ(created_at,'+00:00','+08:00'))=?`, j.ID, billDay).Scan(&anomalyRequests); e != nil {
			return e
		}
		if j.UserID == 0 {
			if _, e = tx.ExecContext(ctx, `INSERT INTO billing_day_status(instance_id,bill_day,active_job_id,calculation_version,status,normal_requests,anomaly_requests,activated_at) VALUES(?,?,?,'request-ledger-v1',?,?,?,?) ON DUPLICATE KEY UPDATE active_job_id=VALUES(active_job_id),calculation_version=VALUES(calculation_version),status=VALUES(status),normal_requests=VALUES(normal_requests),anomaly_requests=VALUES(anomaly_requests),activated_at=VALUES(activated_at)`, j.InstanceID, billDay, j.ID, "complete", normalRequests, anomalyRequests, now); e != nil {
				return e
			}
		}
		insertActive := `INSERT INTO billing_user_daily_active(instance_id,bill_day,user_id,job_id,activated_at) SELECT details.instance_id,details.bill_day,details.user_id,?,? FROM billing_compact_daily_totals details WHERE details.job_id=? AND details.bill_day=?`
		insertArgs := []any{j.ID, now, j.ID, billDay}
		if j.UserID > 0 {
			insertActive += ` AND details.user_id=?`
			insertArgs = append(insertArgs, j.UserID)
		}
		insertActive += ` GROUP BY details.instance_id,details.bill_day,details.user_id ON DUPLICATE KEY UPDATE job_id=VALUES(job_id),activated_at=VALUES(activated_at)`
		if _, e = tx.ExecContext(ctx, insertActive, insertArgs...); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM billing_request_details WHERE job_id=?`, j.ID); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE billing_jobs SET status='complete',finished_at=?,updated_at=? WHERE id=? AND completed_steps>=total_steps`, now, now, j.ID)
	if e != nil {
		return e
	}
	return tx.Commit()
}

func (s Store) BillingDayComplete(ctx context.Context, site string, day time.Time) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_day_status WHERE instance_id=? AND bill_day=? AND status='complete'`, site, day.Format("2006-01-02")).Scan(&count)
	return count > 0, err
}

func (s Store) EarliestCompletedBillingDay(ctx context.Context, site string) (time.Time, error) {
	var day time.Time
	err := s.db.QueryRowContext(ctx, `SELECT MIN(range_from) FROM billing_jobs WHERE instance_id=? AND job_type='generate' AND user_id=0 AND status='complete' AND requested_by<>'scheduler'`, site).Scan(&day)
	return day, err
}

func (s Store) ListCompleteBillingDays(ctx context.Context, site string, from, to time.Time) ([]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT bill_day FROM billing_day_status WHERE instance_id=? AND bill_day>=? AND bill_day<? AND status='complete' ORDER BY bill_day`, site, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []time.Time{}
	for rows.Next() {
		var day time.Time
		if err = rows.Scan(&day); err != nil {
			return nil, err
		}
		items = append(items, day)
	}
	return items, rows.Err()
}

func (s Store) ListBillingRequestDetailGroups(ctx context.Context, jobID string) ([]billing.UserDailyFile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id,bill_day,user_id FROM billing_request_details WHERE job_id=? GROUP BY instance_id,bill_day,user_id ORDER BY bill_day,user_id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.UserDailyFile{}
	for rows.Next() {
		var item billing.UserDailyFile
		item.JobID = jobID
		if err = rows.Scan(&item.InstanceID, &item.BillDay, &item.UserID); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) ListBillingRequestDetails(ctx context.Context, jobID string, day time.Time, userID int64) ([]billing.RequestDetail, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id,job_id,source_log_id,created_at,request_id,user_id,username,token_id,token_name,channel_id,channel_name,model_name,bill_day,billing_mode,matched_tier,prompt_tokens,completion_tokens,cache_read_tokens,cache_write_tokens,cache_write_5m_tokens,cache_write_1h_tokens,input_price,output_price,cache_read_price,cache_write_price,cache_write_5m_price,cache_write_1h_price,per_request_price,input_amount,output_amount,cache_read_amount,cache_write_amount,cache_write_5m_amount,cache_write_1h_amount,total_amount,calculated_quota,logged_quota FROM billing_request_details WHERE job_id=? AND bill_day=? AND user_id=? ORDER BY created_at,source_log_id`, jobID, day.In(billing.BusinessLocation).Format("2006-01-02"), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.RequestDetail{}
	for rows.Next() {
		var item billing.RequestDetail
		var created time.Time
		if err = rows.Scan(&item.InstanceID, &item.JobID, &item.SourceLogID, &created, &item.RequestID, &item.UserID, &item.Username, &item.TokenID, &item.TokenName, &item.ChannelID, &item.ChannelName, &item.ModelName, &item.BillDay, &item.Charge.Mode, &item.Charge.MatchedTier, &item.PromptTokens, &item.CompletionTokens, &item.CacheReadTokens, &item.CacheWriteTokens, &item.CacheWrite5mTokens, &item.CacheWrite1hTokens, &item.Charge.InputPrice, &item.Charge.OutputPrice, &item.Charge.CacheReadPrice, &item.Charge.CacheWritePrice, &item.Charge.CacheWrite5mPrice, &item.Charge.CacheWrite1hPrice, &item.Charge.PerRequestPrice, &item.Charge.InputAmount, &item.Charge.OutputAmount, &item.Charge.CacheReadAmount, &item.Charge.CacheWriteAmount, &item.Charge.CacheWrite5mAmount, &item.Charge.CacheWrite1hAmount, &item.Charge.Total, &item.CalculatedQuota, &item.LoggedQuota); err != nil {
			return nil, err
		}
		item.CreatedUnix = created.Unix()
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) ListBillingChannelRequestDetailGroups(ctx context.Context, jobID string) ([]billing.ChannelDailyFile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id,bill_day,channel_id FROM billing_request_details WHERE job_id=? GROUP BY instance_id,bill_day,channel_id UNION SELECT instance_id,DATE(CONVERT_TZ(created_at,'+00:00','+08:00')),channel_id FROM billing_anomaly_orders WHERE job_id=? GROUP BY instance_id,DATE(CONVERT_TZ(created_at,'+00:00','+08:00')),channel_id ORDER BY bill_day,channel_id`, jobID, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.ChannelDailyFile{}
	for rows.Next() {
		var item billing.ChannelDailyFile
		if err = rows.Scan(&item.InstanceID, &item.BillDay, &item.ChannelID); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) ListBillingChannelRequestDetails(ctx context.Context, jobID string, day time.Time, channelID int64) ([]billing.RequestDetail, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id,job_id,source_log_id,created_at,request_id,user_id,username,token_id,token_name,channel_id,channel_name,model_name,bill_day,billing_mode,matched_tier,prompt_tokens,completion_tokens,cache_read_tokens,cache_write_tokens,cache_write_5m_tokens,cache_write_1h_tokens,input_price,output_price,cache_read_price,cache_write_price,cache_write_5m_price,cache_write_1h_price,per_request_price,input_amount,output_amount,cache_read_amount,cache_write_amount,cache_write_5m_amount,cache_write_1h_amount,total_amount,calculated_quota,logged_quota FROM billing_request_details WHERE job_id=? AND bill_day=? AND channel_id=? ORDER BY created_at,source_log_id`, jobID, day.In(billing.BusinessLocation).Format("2006-01-02"), channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.RequestDetail{}
	for rows.Next() {
		var item billing.RequestDetail
		var created time.Time
		if err = rows.Scan(&item.InstanceID, &item.JobID, &item.SourceLogID, &created, &item.RequestID, &item.UserID, &item.Username, &item.TokenID, &item.TokenName, &item.ChannelID, &item.ChannelName, &item.ModelName, &item.BillDay, &item.Charge.Mode, &item.Charge.MatchedTier, &item.PromptTokens, &item.CompletionTokens, &item.CacheReadTokens, &item.CacheWriteTokens, &item.CacheWrite5mTokens, &item.CacheWrite1hTokens, &item.Charge.InputPrice, &item.Charge.OutputPrice, &item.Charge.CacheReadPrice, &item.Charge.CacheWritePrice, &item.Charge.CacheWrite5mPrice, &item.Charge.CacheWrite1hPrice, &item.Charge.PerRequestPrice, &item.Charge.InputAmount, &item.Charge.OutputAmount, &item.Charge.CacheReadAmount, &item.Charge.CacheWriteAmount, &item.Charge.CacheWrite5mAmount, &item.Charge.CacheWrite1hAmount, &item.Charge.Total, &item.CalculatedQuota, &item.LoggedQuota); err != nil {
			return nil, err
		}
		item.CreatedUnix = created.Unix()
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) ListBillingChannelAnomalyDetails(ctx context.Context, jobID string, day time.Time, channelID int64) ([]billing.AnomalyOrder, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id,source_log_id,job_id,created_at,request_id,upstream_request_id,user_id,username,token_id,token_name,channel_id,channel_name,model_name,group_name,prompt_tokens,completion_tokens,cache_tokens,cache_write_tokens,cache_write_5m_tokens,cache_write_1h_tokens,quota,max_context_tokens,input_price,output_price,cache_price,cache_write_price,input_amount,output_amount,cache_amount,cache_write_amount,reference_amount,actual_amount,reasons,detected_at FROM billing_anomaly_orders WHERE job_id=? AND channel_id=? AND DATE(CONVERT_TZ(created_at,'+00:00','+08:00'))=? ORDER BY created_at,source_log_id`, jobID, channelID, day.In(billing.BusinessLocation).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.AnomalyOrder{}
	for rows.Next() {
		var v billing.AnomalyOrder
		if err = rows.Scan(&v.InstanceID, &v.SourceLogID, &v.JobID, &v.CreatedAt, &v.RequestID, &v.UpstreamRequestID, &v.UserID, &v.Username, &v.TokenID, &v.TokenName, &v.ChannelID, &v.ChannelName, &v.ModelName, &v.GroupName, &v.PromptTokens, &v.CompletionTokens, &v.CacheTokens, &v.CacheWriteTokens, &v.CacheWrite5mTokens, &v.CacheWrite1hTokens, &v.Quota, &v.MaxContextTokens, &v.InputPrice, &v.OutputPrice, &v.CachePrice, &v.CacheWritePrice, &v.InputAmount, &v.OutputAmount, &v.CacheAmount, &v.CacheWriteAmount, &v.ReferenceAmount, &v.ActualAmount, &v.Reasons, &v.DetectedAt); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func (s Store) PutBillingChannelDailyFile(ctx context.Context, item billing.ChannelDailyFile) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO billing_channel_daily_files(job_id,instance_id,bill_day,channel_id,relative_path,file_size,sha256,created_at) VALUES(?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE relative_path=VALUES(relative_path),file_size=VALUES(file_size),sha256=VALUES(sha256),created_at=VALUES(created_at)`, item.JobID, item.InstanceID, item.BillDay, item.ChannelID, item.RelativePath, item.FileSize, item.SHA256, item.CreatedAt)
	return err
}

func (s Store) ActiveBillingChannelDailyFile(ctx context.Context, site string, day time.Time, channelID int64) (billing.ChannelDailyFile, error) {
	var item billing.ChannelDailyFile
	err := s.db.QueryRowContext(ctx, `SELECT files.job_id,files.instance_id,files.bill_day,files.channel_id,files.relative_path,files.file_size,files.sha256,files.created_at FROM billing_day_status day_status JOIN billing_channel_daily_files files ON files.job_id=day_status.active_job_id AND files.instance_id=day_status.instance_id AND files.bill_day=day_status.bill_day WHERE day_status.instance_id=? AND day_status.bill_day=? AND day_status.status='complete' AND files.channel_id=?`, site, day.In(billing.BusinessLocation).Format("2006-01-02"), channelID).Scan(&item.JobID, &item.InstanceID, &item.BillDay, &item.ChannelID, &item.RelativePath, &item.FileSize, &item.SHA256, &item.CreatedAt)
	return item, err
}

func (s Store) PutBillingUserDailyFile(ctx context.Context, item billing.UserDailyFile) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO billing_user_daily_files(job_id,instance_id,bill_day,user_id,relative_path,file_size,sha256,created_at) VALUES(?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE relative_path=VALUES(relative_path),file_size=VALUES(file_size),sha256=VALUES(sha256),created_at=VALUES(created_at)`, item.JobID, item.InstanceID, item.BillDay, item.UserID, item.RelativePath, item.FileSize, item.SHA256, item.CreatedAt)
	return err
}

func (s Store) ActiveBillingUserDailyFile(ctx context.Context, site string, day time.Time, userID int64) (billing.UserDailyFile, error) {
	var item billing.UserDailyFile
	err := s.db.QueryRowContext(ctx, `SELECT files.job_id,files.instance_id,files.bill_day,files.user_id,files.relative_path,files.file_size,files.sha256,files.created_at FROM billing_user_daily_active active JOIN billing_user_daily_files files ON files.job_id=active.job_id AND files.instance_id=active.instance_id AND files.bill_day=active.bill_day AND files.user_id=active.user_id WHERE active.instance_id=? AND active.bill_day=? AND active.user_id=?`, site, day, userID).Scan(&item.JobID, &item.InstanceID, &item.BillDay, &item.UserID, &item.RelativePath, &item.FileSize, &item.SHA256, &item.CreatedAt)
	return item, err
}

func (s Store) BillingActiveDays(ctx context.Context, site string, userID int64, from, to time.Time) (map[string]string, error) {
	fromDay := from.In(billing.BusinessLocation).Format("2006-01-02")
	toDay := to.In(billing.BusinessLocation).Format("2006-01-02")
	// Whole-site generation must use the same authoritative coverage ledger as
	// user/channel bill pages. billing_active_versions is legacy projection data
	// and can contain rows for dates that were never completed by the current
	// request-ledger calculation.
	query := `SELECT bill_day,active_job_id FROM billing_day_status WHERE instance_id=? AND bill_day>=? AND bill_day<? AND status='complete'`
	args := []any{site, fromDay, toDay}
	if userID > 0 {
		query = `SELECT bill_day,job_id FROM billing_user_daily_active WHERE instance_id=? AND user_id=? AND bill_day>=? AND bill_day<?`
		args = []any{site, userID, fromDay, toDay}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var day time.Time
		var jobID string
		if err = rows.Scan(&day, &jobID); err != nil {
			return nil, err
		}
		result[day.Format("2006-01-02")] = jobID
	}
	return result, rows.Err()
}

func (s Store) ListExpiredInactiveBillingFiles(ctx context.Context, cutoff time.Time, limit int) ([]billing.UserDailyFile, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT files.job_id,files.instance_id,files.bill_day,files.user_id,files.relative_path,files.file_size,files.sha256,files.created_at FROM billing_user_daily_files files LEFT JOIN billing_user_daily_active active ON active.job_id=files.job_id AND active.instance_id=files.instance_id AND active.bill_day=files.bill_day AND active.user_id=files.user_id WHERE files.created_at<? AND active.job_id IS NULL ORDER BY files.created_at LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.UserDailyFile{}
	for rows.Next() {
		var item billing.UserDailyFile
		if err = rows.Scan(&item.JobID, &item.InstanceID, &item.BillDay, &item.UserID, &item.RelativePath, &item.FileSize, &item.SHA256, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) DeleteBillingUserDailyFile(ctx context.Context, item billing.UserDailyFile) error {
	_, err := s.db.ExecContext(ctx, `DELETE files FROM billing_user_daily_files files LEFT JOIN billing_user_daily_active active ON active.job_id=files.job_id AND active.instance_id=files.instance_id AND active.bill_day=files.bill_day AND active.user_id=files.user_id WHERE files.job_id=? AND files.bill_day=? AND files.user_id=? AND active.job_id IS NULL`, item.JobID, item.BillDay, item.UserID)
	return err
}

func (s Store) ListExpiredInactiveBillingChannelFiles(ctx context.Context, cutoff time.Time, limit int) ([]billing.ChannelDailyFile, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT files.job_id,files.instance_id,files.bill_day,files.channel_id,files.relative_path,files.file_size,files.sha256,files.created_at FROM billing_channel_daily_files files LEFT JOIN billing_day_status active ON active.active_job_id=files.job_id AND active.instance_id=files.instance_id AND active.bill_day=files.bill_day AND active.status='complete' WHERE files.created_at<? AND active.active_job_id IS NULL ORDER BY files.created_at LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.ChannelDailyFile{}
	for rows.Next() {
		var item billing.ChannelDailyFile
		if err = rows.Scan(&item.JobID, &item.InstanceID, &item.BillDay, &item.ChannelID, &item.RelativePath, &item.FileSize, &item.SHA256, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) DeleteBillingChannelDailyFile(ctx context.Context, item billing.ChannelDailyFile) error {
	_, err := s.db.ExecContext(ctx, `DELETE files FROM billing_channel_daily_files files LEFT JOIN billing_day_status active ON active.active_job_id=files.job_id AND active.instance_id=files.instance_id AND active.bill_day=files.bill_day AND active.status='complete' WHERE files.job_id=? AND files.bill_day=? AND files.channel_id=? AND active.active_job_id IS NULL`, item.JobID, item.BillDay, item.ChannelID)
	return err
}

func (s Store) ListBillingDailyOverview(ctx context.Context, site string, from, to time.Time, limit int) ([]billing.DailyOverview, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	query := `SELECT active.instance_id,active.bill_day,COUNT(*),SUM((SELECT COALESCE(SUM(details.request_count),0) FROM billing_compact_daily_totals details WHERE details.job_id=active.job_id AND details.bill_day=active.bill_day AND details.user_id=active.user_id)),CAST(SUM((SELECT COALESCE(SUM(details.total_amount),0) FROM billing_compact_daily_totals details WHERE details.job_id=active.job_id AND details.bill_day=active.bill_day AND details.user_id=active.user_id)) AS CHAR),SUM((SELECT COUNT(*) FROM billing_anomaly_orders anomaly WHERE anomaly.job_id=active.job_id AND anomaly.user_id=active.user_id AND DATE(CONVERT_TZ(anomaly.created_at,'+00:00','+08:00'))=active.bill_day)),SUM(EXISTS(SELECT 1 FROM billing_user_daily_files files WHERE files.job_id=active.job_id AND files.bill_day=active.bill_day AND files.user_id=active.user_id)),MAX(active.activated_at) FROM billing_user_daily_active active WHERE active.bill_day>=? AND active.bill_day<?`
	args := []any{from, to}
	if site != "" {
		query += ` AND active.instance_id=?`
		args = append(args, site)
	}
	query += ` GROUP BY active.instance_id,active.bill_day ORDER BY active.bill_day DESC,active.instance_id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.DailyOverview{}
	for rows.Next() {
		var item billing.DailyOverview
		if err = rows.Scan(&item.InstanceID, &item.Day, &item.UserCount, &item.RequestCount, &item.Amount, &item.AnomalyRows, &item.FileCount, &item.ActivatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) ListBillingUserBillDays(ctx context.Context, site string, from, to time.Time, userID int64, search string, limit int) ([]billing.UserBillDay, error) {
	if limit <= 0 || limit > 100000 {
		limit = 100000
	}
	query := `SELECT active.instance_id,active.job_id,active.bill_day,active.user_id,COALESCE(MAX(details.username),''),COALESCE(details.model_name,''),COALESCE(SUM(details.request_count),0),COALESCE(SUM(details.prompt_tokens),0),COALESCE(SUM(details.completion_tokens),0),COALESCE(SUM(details.cache_read_tokens),0),COALESCE(SUM(details.cache_write_tokens),0),CAST(COALESCE(SUM(details.total_amount),0) AS CHAR),(SELECT COUNT(*) FROM billing_anomaly_orders anomaly WHERE anomaly.job_id=active.job_id AND anomaly.user_id=active.user_id AND anomaly.model_name=COALESCE(details.model_name,'') AND DATE(CONVERT_TZ(anomaly.created_at,'+00:00','+08:00'))=active.bill_day),(SELECT CAST(COALESCE(SUM(anomaly.actual_amount),0) AS CHAR) FROM billing_anomaly_orders anomaly WHERE anomaly.job_id=active.job_id AND anomaly.user_id=active.user_id AND anomaly.model_name=COALESCE(details.model_name,'') AND DATE(CONVERT_TZ(anomaly.created_at,'+00:00','+08:00'))=active.bill_day),active.activated_at FROM billing_user_daily_active active LEFT JOIN billing_compact_daily_totals details ON details.job_id=active.job_id AND details.bill_day=active.bill_day AND details.user_id=active.user_id WHERE active.instance_id=? AND active.bill_day>=? AND active.bill_day<?`
	args := []any{site, from, to}
	if userID > 0 {
		query += ` AND active.user_id=?`
		args = append(args, userID)
	}
	if search = strings.TrimSpace(search); search != "" {
		query += ` AND (CAST(active.user_id AS CHAR)=? OR EXISTS(SELECT 1 FROM billing_compact_daily_totals named WHERE named.job_id=active.job_id AND named.bill_day=active.bill_day AND named.user_id=active.user_id AND named.username LIKE ?))`
		args = append(args, search, "%"+search+"%")
	}
	query += ` GROUP BY active.instance_id,active.job_id,active.bill_day,active.user_id,details.model_name,active.activated_at ORDER BY active.bill_day DESC,active.user_id,details.model_name LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.UserBillDay{}
	for rows.Next() {
		var item billing.UserBillDay
		if err = rows.Scan(&item.InstanceID, &item.JobID, &item.Day, &item.UserID, &item.Username, &item.ModelName, &item.RequestCount, &item.PromptTokens, &item.CompletionTokens, &item.CacheReadTokens, &item.CacheWriteTokens, &item.Amount, &item.AnomalyRows, &item.AnomalyAmount, &item.ActivatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) ListBillingUserTokenBillDays(ctx context.Context, site string, from, to time.Time, userID int64, limit int) ([]billing.UserTokenBillDay, error) {
	if limit <= 0 || limit > 100000 {
		limit = 100000
	}
	query := `SELECT active.instance_id,active.job_id,active.bill_day,active.user_id,COALESCE(MAX(details.username),''),details.token_id,COALESCE(MAX(details.token_name),''),details.model_name,SUM(details.request_count),COALESCE(SUM(details.prompt_tokens),0),COALESCE(SUM(details.completion_tokens),0),COALESCE(SUM(details.cache_read_tokens),0),COALESCE(SUM(details.cache_write_tokens),0),CAST(COALESCE(SUM(details.total_amount),0) AS CHAR),(SELECT COUNT(*) FROM billing_anomaly_orders anomaly WHERE anomaly.job_id=active.job_id AND anomaly.user_id=active.user_id AND anomaly.token_id=details.token_id AND anomaly.model_name=details.model_name AND DATE(CONVERT_TZ(anomaly.created_at,'+00:00','+08:00'))=active.bill_day),(SELECT CAST(COALESCE(SUM(anomaly.actual_amount),0) AS CHAR) FROM billing_anomaly_orders anomaly WHERE anomaly.job_id=active.job_id AND anomaly.user_id=active.user_id AND anomaly.token_id=details.token_id AND anomaly.model_name=details.model_name AND DATE(CONVERT_TZ(anomaly.created_at,'+00:00','+08:00'))=active.bill_day),active.activated_at FROM billing_user_daily_active active JOIN billing_compact_daily_totals details ON details.job_id=active.job_id AND details.bill_day=active.bill_day AND details.user_id=active.user_id WHERE active.instance_id=? AND active.bill_day>=? AND active.bill_day<? AND active.user_id=? GROUP BY active.instance_id,active.job_id,active.bill_day,active.user_id,details.token_id,details.model_name,active.activated_at ORDER BY details.token_id,active.bill_day DESC,details.model_name LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, site, from, to, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.UserTokenBillDay{}
	for rows.Next() {
		var item billing.UserTokenBillDay
		if err = rows.Scan(&item.InstanceID, &item.JobID, &item.Day, &item.UserID, &item.Username, &item.TokenID, &item.TokenName, &item.ModelName, &item.RequestCount, &item.PromptTokens, &item.CompletionTokens, &item.CacheReadTokens, &item.CacheWriteTokens, &item.Amount, &item.AnomalyRows, &item.AnomalyAmount, &item.ActivatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
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
