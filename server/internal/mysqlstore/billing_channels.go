package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"time"
)

func (s Store) LatestBillingJob(ctx context.Context, site, jobType string, from, to time.Time) (billing.Job, error) {
	var j billing.Job
	err := s.db.QueryRowContext(ctx, `SELECT id,instance_id,job_type,user_id,range_from,range_to,status,total_steps,completed_steps,abnormal_rows,error_message,output_path,requested_by,created_at,updated_at FROM billing_jobs WHERE instance_id=? AND job_type=? AND range_from=? AND range_to=? ORDER BY created_at DESC LIMIT 1`, site, jobType, from, to).Scan(&j.ID, &j.InstanceID, &j.JobType, &j.UserID, &j.From, &j.To, &j.Status, &j.TotalSteps, &j.CompletedSteps, &j.AbnormalRows, &j.ErrorMessage, &j.OutputPath, &j.RequestedBy, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

func (s Store) QueryBillingChannelAggregates(ctx context.Context, site string, from, to time.Time, channelID int64) ([]billing.AggregateRow, error) {
	dayFrom, dayTo := billingDayBounds(from, to)
	q := `SELECT v.instance_id,v.channel_id,MAX(v.channel_name),v.model_name,v.group_name,v.tier_from,v.day,SUM(v.request_count),SUM(v.prompt_tokens),SUM(v.completion_tokens),SUM(v.cache_tokens),SUM(v.cache_write_tokens),SUM(v.cache_write_5m_tokens),SUM(v.cache_write_1h_tokens),SUM(v.quota) FROM billing_channel_daily_versions v JOIN billing_active_versions a ON a.instance_id=v.instance_id AND a.day=v.day AND a.job_id=v.job_id WHERE v.instance_id=? AND v.day>=? AND v.day<?`
	args := []any{site, dayFrom, dayTo}
	if channelID > 0 {
		q += ` AND v.channel_id=?`
		args = append(args, channelID)
	}
	q += ` GROUP BY v.instance_id,v.channel_id,v.model_name,v.group_name,v.tier_from,v.day ORDER BY v.channel_id,v.day,v.model_name,v.group_name,v.tier_from`
	rows, e := s.db.QueryContext(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []billing.AggregateRow{}
	for rows.Next() {
		var v billing.AggregateRow
		if e = rows.Scan(&v.InstanceID, &v.UserID, &v.Username, &v.ModelName, &v.GroupName, &v.TierFrom, &v.Day, &v.RequestCount, &v.PromptTokens, &v.CompletionTokens, &v.CacheTokens, &v.CacheWriteTokens, &v.CacheWrite5mTokens, &v.CacheWrite1hTokens, &v.Quota); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s Store) QueryBillingChannelAggregatesForJob(ctx context.Context, jobID string, channelID int64) ([]billing.AggregateRow, error) {
	q := `SELECT instance_id,channel_id,MAX(channel_name),model_name,group_name,tier_from,day,SUM(request_count),SUM(prompt_tokens),SUM(completion_tokens),SUM(cache_tokens),SUM(cache_write_tokens),SUM(cache_write_5m_tokens),SUM(cache_write_1h_tokens),SUM(quota) FROM billing_channel_daily_versions WHERE job_id=?`
	args := []any{jobID}
	if channelID > 0 {
		q += ` AND channel_id=?`
		args = append(args, channelID)
	}
	q += ` GROUP BY instance_id,channel_id,model_name,group_name,tier_from,day ORDER BY channel_id,day,model_name,group_name,tier_from`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []billing.AggregateRow{}
	for rows.Next() {
		var v billing.AggregateRow
		if err = rows.Scan(&v.InstanceID, &v.UserID, &v.Username, &v.ModelName, &v.GroupName, &v.TierFrom, &v.Day, &v.RequestCount, &v.PromptTokens, &v.CompletionTokens, &v.CacheTokens, &v.CacheWriteTokens, &v.CacheWrite5mTokens, &v.CacheWrite1hTokens, &v.Quota); err != nil {
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
