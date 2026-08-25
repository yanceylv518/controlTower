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
	q := `SELECT details.instance_id,details.channel_id,MAX(details.channel_name),details.model_name,'' group_name,0 tier_from,details.bill_day,SUM(details.request_count),SUM(details.prompt_tokens),SUM(details.completion_tokens),SUM(details.cache_read_tokens),SUM(details.cache_write_tokens),SUM(details.cache_write_5m_tokens),SUM(details.cache_write_1h_tokens),SUM(details.calculated_quota),CAST(SUM(details.total_amount) AS CHAR) FROM billing_compact_daily_totals details JOIN billing_user_daily_active active ON active.instance_id=details.instance_id AND active.bill_day=details.bill_day AND active.user_id=details.user_id AND active.job_id=details.job_id WHERE details.instance_id=? AND details.bill_day>=? AND details.bill_day<?`
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
	q := `SELECT instance_id,channel_id,MAX(channel_name),model_name,'' group_name,0 tier_from,bill_day,SUM(request_count),SUM(prompt_tokens),SUM(completion_tokens),SUM(cache_read_tokens),SUM(cache_write_tokens),SUM(cache_write_5m_tokens),SUM(cache_write_1h_tokens),SUM(calculated_quota),CAST(SUM(total_amount) AS CHAR) FROM billing_compact_daily_totals WHERE job_id=?`
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

func (s Store) QueryBillingChannelAnomalies(ctx context.Context, site string, from, to time.Time) ([]billing.ChannelAnomalyRow, error) {
	fromDay, toDay := billingDayBounds(from, to)
	rows, err := s.db.QueryContext(ctx, `SELECT anomaly.channel_id,MAX(anomaly.channel_name),day_status.bill_day,anomaly.model_name,COUNT(*),CAST(COALESCE(SUM(anomaly.actual_amount),0) AS CHAR) FROM billing_anomaly_orders anomaly JOIN billing_day_status day_status ON day_status.instance_id=anomaly.instance_id AND day_status.active_job_id=anomaly.job_id AND day_status.bill_day=DATE(CONVERT_TZ(anomaly.created_at,'+00:00','+08:00')) AND day_status.status='complete' WHERE anomaly.instance_id=? AND day_status.bill_day>=? AND day_status.bill_day<? GROUP BY anomaly.channel_id,day_status.bill_day,anomaly.model_name ORDER BY day_status.bill_day,anomaly.channel_id,anomaly.model_name`, site, fromDay, toDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.ChannelAnomalyRow{}
	for rows.Next() {
		var item billing.ChannelAnomalyRow
		if err = rows.Scan(&item.ChannelID, &item.ChannelName, &item.Day, &item.ModelName, &item.Rows, &item.Amount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) QueryBillingChannelRequestDetails(ctx context.Context, site string, channelID int64, from, to time.Time) ([]billing.RequestDetail, error) {
	fromDay, toDay := billingDayBounds(from, to)
	rows, err := s.db.QueryContext(ctx, `SELECT details.instance_id,details.job_id,details.source_log_id,details.created_at,details.request_id,details.user_id,details.username,details.token_id,details.token_name,details.channel_id,details.channel_name,details.model_name,details.bill_day,details.billing_mode,details.matched_tier,details.prompt_tokens,details.completion_tokens,details.cache_read_tokens,details.cache_write_tokens,details.cache_write_5m_tokens,details.cache_write_1h_tokens,details.input_price,details.output_price,details.cache_read_price,details.cache_write_price,details.cache_write_5m_price,details.cache_write_1h_price,details.per_request_price,details.input_amount,details.output_amount,details.cache_read_amount,details.cache_write_amount,details.cache_write_5m_amount,details.cache_write_1h_amount,details.total_amount,details.calculated_quota,details.logged_quota FROM billing_request_details details JOIN billing_user_daily_active active ON active.instance_id=details.instance_id AND active.bill_day=details.bill_day AND active.user_id=details.user_id AND active.job_id=details.job_id WHERE details.instance_id=? AND details.channel_id=? AND details.bill_day>=? AND details.bill_day<? ORDER BY details.created_at,details.source_log_id`, site, channelID, fromDay, toDay)
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

func (s Store) QueryBillingChannelAnomalyOrders(ctx context.Context, site string, channelID int64, from, to time.Time) ([]billing.AnomalyOrder, error) {
	fromDay, toDay := billingDayBounds(from, to)
	rows, err := s.db.QueryContext(ctx, `SELECT anomaly.instance_id,anomaly.source_log_id,anomaly.job_id,anomaly.created_at,anomaly.request_id,anomaly.upstream_request_id,anomaly.user_id,anomaly.username,anomaly.token_id,anomaly.token_name,anomaly.channel_id,anomaly.channel_name,anomaly.model_name,anomaly.group_name,anomaly.prompt_tokens,anomaly.completion_tokens,anomaly.cache_tokens,anomaly.cache_write_tokens,anomaly.cache_write_5m_tokens,anomaly.cache_write_1h_tokens,anomaly.quota,anomaly.max_context_tokens,anomaly.input_price,anomaly.output_price,anomaly.cache_price,anomaly.cache_write_price,anomaly.input_amount,anomaly.output_amount,anomaly.cache_amount,anomaly.cache_write_amount,anomaly.reference_amount,anomaly.actual_amount,anomaly.reasons,anomaly.detected_at FROM billing_anomaly_orders anomaly JOIN billing_day_status day_status ON day_status.instance_id=anomaly.instance_id AND day_status.active_job_id=anomaly.job_id AND day_status.bill_day=DATE(CONVERT_TZ(anomaly.created_at,'+00:00','+08:00')) AND day_status.status='complete' WHERE anomaly.instance_id=? AND anomaly.channel_id=? AND day_status.bill_day>=? AND day_status.bill_day<? ORDER BY anomaly.created_at,anomaly.source_log_id`, site, channelID, fromDay, toDay)
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
