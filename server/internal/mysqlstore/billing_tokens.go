package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
)

func (s Store) QueryBillingTokenRows(ctx context.Context, jobID string, userID, tokenID int64) ([]billing.TokenDailyRow, error) {
	query := `SELECT instance_id,user_id,token_id,token_name,username,model_name,group_name,tier_from,day,request_count,prompt_tokens,completion_tokens,cache_tokens,cache_write_tokens,cache_write_5m_tokens,cache_write_1h_tokens,quota,updated_at FROM billing_token_daily_versions WHERE job_id=? AND user_id=?`
	args := []any{jobID, userID}
	if tokenID >= 0 {
		query += ` AND token_id=?`
		args = append(args, tokenID)
	}
	query += ` ORDER BY day DESC,model_name,group_name,tier_from`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []billing.TokenDailyRow{}
	for rows.Next() {
		var v billing.TokenDailyRow
		if err = rows.Scan(&v.InstanceID, &v.UserID, &v.TokenID, &v.TokenName, &v.Username, &v.ModelName, &v.GroupName, &v.TierFrom, &v.Day, &v.RequestCount, &v.PromptTokens, &v.CompletionTokens, &v.CacheTokens, &v.CacheWriteTokens, &v.CacheWrite5mTokens, &v.CacheWrite1hTokens, &v.Quota, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
