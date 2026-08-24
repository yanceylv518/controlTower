package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"time"
)

func (s Store) LatestBillingJob(ctx context.Context, site, jobType string, from, to time.Time) (billing.Job, error) {
	var j billing.Job
	err := s.db.QueryRowContext(ctx, `SELECT id,instance_id,job_type,user_id,range_from,range_to,status,total_steps,completed_steps,abnormal_rows,error_message,output_path,requested_by,created_at,updated_at FROM billing_jobs WHERE instance_id=? AND job_type=? AND user_id=0 AND range_from=? AND range_to=? ORDER BY created_at DESC LIMIT 1`, site, jobType, from, to).Scan(&j.ID, &j.InstanceID, &j.JobType, &j.UserID, &j.From, &j.To, &j.Status, &j.TotalSteps, &j.CompletedSteps, &j.AbnormalRows, &j.ErrorMessage, &j.OutputPath, &j.RequestedBy, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

func (s Store) LatestCompletedBillingJob(ctx context.Context, site, jobType string) (billing.Job, error) {
	var j billing.Job
	err := s.db.QueryRowContext(ctx, `SELECT id,instance_id,job_type,user_id,range_from,range_to,status,total_steps,completed_steps,abnormal_rows,error_message,output_path,requested_by,created_at,updated_at FROM billing_jobs WHERE instance_id=? AND job_type=? AND user_id=0 AND status='complete' ORDER BY updated_at DESC,created_at DESC LIMIT 1`, site, jobType).Scan(&j.ID, &j.InstanceID, &j.JobType, &j.UserID, &j.From, &j.To, &j.Status, &j.TotalSteps, &j.CompletedSteps, &j.AbnormalRows, &j.ErrorMessage, &j.OutputPath, &j.RequestedBy, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

func (s Store) LatestCoveringBillingJob(ctx context.Context, site, jobType string, from, to time.Time) (billing.Job, error) {
	var j billing.Job
	err := s.db.QueryRowContext(ctx, `SELECT id,instance_id,job_type,user_id,range_from,range_to,status,total_steps,completed_steps,abnormal_rows,error_message,output_path,requested_by,created_at,updated_at FROM billing_jobs WHERE instance_id=? AND job_type=? AND user_id=0 AND status='complete' AND range_from<=? AND range_to>=? ORDER BY updated_at DESC,created_at DESC LIMIT 1`, site, jobType, from, to).Scan(&j.ID, &j.InstanceID, &j.JobType, &j.UserID, &j.From, &j.To, &j.Status, &j.TotalSteps, &j.CompletedSteps, &j.AbnormalRows, &j.ErrorMessage, &j.OutputPath, &j.RequestedBy, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

func (s Store) QueryBillingChannelAggregates(ctx context.Context, site string, from, to time.Time, channelID int64) ([]billing.AggregateRow, error) {
	dayFrom, dayTo := billingDayBounds(from, to)
	q := `SELECT details.instance_id,details.channel_id,MAX(details.channel_name),details.model_name,'' group_name,0 tier_from,details.bill_day,COUNT(*),SUM(details.prompt_tokens),SUM(details.completion_tokens),SUM(details.cache_read_tokens),SUM(details.cache_write_tokens),SUM(details.cache_write_5m_tokens),SUM(details.cache_write_1h_tokens),SUM(details.calculated_quota),CAST(SUM(details.total_amount) AS CHAR) FROM billing_request_details details JOIN billing_user_daily_active active ON active.instance_id=details.instance_id AND active.bill_day=details.bill_day AND active.user_id=details.user_id AND active.job_id=details.job_id WHERE details.instance_id=? AND details.bill_day>=? AND details.bill_day<?`
	args := []any{site, dayFrom, dayTo}
	if channelID > 0 {
		q += ` AND details.channel_id=?`
		args = append(args, channelID)
	}
	q += ` GROUP BY details.instance_id,details.channel_id,details.model_name,details.bill_day ORDER BY details.channel_id,details.bill_day,details.model_name`
	rows, e := s.db.QueryContext(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []billing.AggregateRow{}
	for rows.Next() {
		var v billing.AggregateRow
		if e = rows.Scan(&v.InstanceID, &v.UserID, &v.Username, &v.ModelName, &v.GroupName, &v.TierFrom, &v.Day, &v.RequestCount, &v.PromptTokens, &v.CompletionTokens, &v.CacheTokens, &v.CacheWriteTokens, &v.CacheWrite5mTokens, &v.CacheWrite1hTokens, &v.Quota, &v.Amount); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s Store) QueryBillingChannelAggregatesForJob(ctx context.Context, jobID string, channelID int64) ([]billing.AggregateRow, error) {
	q := `SELECT instance_id,channel_id,MAX(channel_name),model_name,'' group_name,0 tier_from,bill_day,COUNT(*),SUM(prompt_tokens),SUM(completion_tokens),SUM(cache_read_tokens),SUM(cache_write_tokens),SUM(cache_write_5m_tokens),SUM(cache_write_1h_tokens),SUM(calculated_quota),CAST(SUM(total_amount) AS CHAR) FROM billing_request_details WHERE job_id=?`
	args := []any{jobID}
	if channelID > 0 {
		q += ` AND channel_id=?`
		args = append(args, channelID)
	}
	q += ` GROUP BY instance_id,channel_id,model_name,bill_day ORDER BY channel_id,bill_day,model_name`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []billing.AggregateRow{}
	for rows.Next() {
		var v billing.AggregateRow
		if err = rows.Scan(&v.InstanceID, &v.UserID, &v.Username, &v.ModelName, &v.GroupName, &v.TierFrom, &v.Day, &v.RequestCount, &v.PromptTokens, &v.CompletionTokens, &v.CacheTokens, &v.CacheWriteTokens, &v.CacheWrite5mTokens, &v.CacheWrite1hTokens, &v.Quota, &v.Amount); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s Store) ListBillingChannelSettings(ctx context.Context, site string) (map[int64]billing.ChannelSetting, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT instance_id,channel_id,discount,updated_at,updated_by FROM billing_channel_settings WHERE instance_id=?`, site)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[int64]billing.ChannelSetting{}
	for rows.Next() {
		var v billing.ChannelSetting
		if e = rows.Scan(&v.InstanceID, &v.ChannelID, &v.Discount, &v.UpdatedAt, &v.UpdatedBy); e != nil {
			return nil, e
		}
		out[v.ChannelID] = v
	}
	return out, rows.Err()
}
func (s Store) PutBillingChannelSetting(ctx context.Context, v billing.ChannelSetting) error {
	_, e := s.db.ExecContext(ctx, `INSERT INTO billing_channel_settings(instance_id,channel_id,discount,updated_at,updated_by) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE discount=VALUES(discount),updated_at=VALUES(updated_at),updated_by=VALUES(updated_by)`, v.InstanceID, v.ChannelID, v.Discount, v.UpdatedAt, v.UpdatedBy)
	return e
}
