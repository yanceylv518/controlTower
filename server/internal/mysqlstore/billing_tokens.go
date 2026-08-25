package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"time"
)

func (s Store) QueryBillingTokenRows(ctx context.Context, jobID string, userID, tokenID int64, from, to time.Time) ([]billing.TokenDailyRow, error) {
	dayFrom, dayTo := billingDayBounds(from, to)
	query := `SELECT instance_id,user_id,token_id,MAX(token_name),MAX(username),model_name,'' group_name,0 tier_from,bill_day,SUM(request_count),SUM(prompt_tokens),SUM(completion_tokens),SUM(cache_read_tokens),SUM(cache_write_tokens),SUM(cache_write_5m_tokens),SUM(cache_write_1h_tokens),SUM(calculated_quota),CAST(SUM(total_amount) AS CHAR),MAX(updated_at) FROM billing_compact_daily_totals WHERE job_id=? AND user_id=? AND bill_day>=? AND bill_day<?`
	args := []any{jobID, userID, dayFrom, dayTo}
	if tokenID >= 0 {
		query += ` AND token_id=?`
		args = append(args, tokenID)
	}
	query += ` GROUP BY instance_id,user_id,token_id,model_name,bill_day ORDER BY bill_day DESC,model_name`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []billing.TokenDailyRow{}
	for rows.Next() {
		var v billing.TokenDailyRow
		if err = rows.Scan(&v.InstanceID, &v.UserID, &v.TokenID, &v.TokenName, &v.Username, &v.ModelName, &v.GroupName, &v.TierFrom, &v.Day, &v.RequestCount, &v.PromptTokens, &v.CompletionTokens, &v.CacheTokens, &v.CacheWriteTokens, &v.CacheWrite5mTokens, &v.CacheWrite1hTokens, &v.Quota, &v.Amount, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
