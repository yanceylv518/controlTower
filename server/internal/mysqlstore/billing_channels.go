package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"time"
)

func (s Store) QueryBillingChannelAggregates(ctx context.Context, site string, from, to time.Time, channelID int64) ([]billing.AggregateRow, error) {
	q := `SELECT v.instance_id,v.channel_id,MAX(v.channel_name),v.model_name,v.group_name,v.tier_from,v.day,SUM(v.request_count),SUM(v.prompt_tokens),SUM(v.completion_tokens),SUM(v.cache_tokens),SUM(v.quota) FROM billing_channel_daily_versions v JOIN billing_active_versions a ON a.instance_id=v.instance_id AND a.day=v.day AND a.job_id=v.job_id WHERE v.instance_id=? AND v.day>=? AND v.day<?`
	args := []any{site, from, to}
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
		if e = rows.Scan(&v.InstanceID, &v.UserID, &v.Username, &v.ModelName, &v.GroupName, &v.TierFrom, &v.Day, &v.RequestCount, &v.PromptTokens, &v.CompletionTokens, &v.CacheTokens, &v.Quota); e != nil {
			return nil, e
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
