package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"time"
)

func (s Store) QueryBillingTokenRows(ctx context.Context, jobID string, userID, tokenID int64, from, to time.Time) ([]billing.TokenDailyRow, error) {
	dayFrom, dayTo := billingDayBounds(from, to)
	query := `SELECT instance_id,user_id,token_id,MAX(token_name),MAX(username),model_name,'' group_name,0 tier_from,bill_day,SUM(request_count),SUM(prompt_tokens),SUM(completion_tokens),SUM(image_input_tokens),SUM(image_output_tokens),SUM(audio_input_tokens),SUM(audio_output_tokens),SUM(cache_read_tokens),SUM(cache_write_tokens),SUM(cache_write_5m_tokens),SUM(cache_write_1h_tokens),SUM(calculated_quota),CAST(SUM(total_amount) AS CHAR),CASE WHEN SUM(CASE WHEN before_known_count=request_count OR settlement_discount IN ('','1','1.000000') THEN 0 ELSE 1 END)=0 THEN CAST(SUM(CASE WHEN before_known_count=request_count THEN before_amount ELSE total_amount END) AS CHAR) ELSE '' END,CASE WHEN MIN(COALESCE(NULLIF(settlement_discount,''),'1.000000'))=MAX(COALESCE(NULLIF(settlement_discount,''),'1.000000')) THEN MIN(COALESCE(NULLIF(settlement_discount,''),'1.000000')) ELSE 'mixed' END,MAX(updated_at),JSON_ARRAYAGG(unit_prices) FROM billing_compact_daily_totals WHERE job_id=? AND user_id=? AND bill_day>=? AND bill_day<?`
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
		var prices []byte
		if err = rows.Scan(&v.InstanceID, &v.UserID, &v.TokenID, &v.TokenName, &v.Username, &v.ModelName, &v.GroupName, &v.TierFrom, &v.Day, &v.RequestCount, &v.PromptTokens, &v.CompletionTokens, &v.ImageInputTokens, &v.ImageOutputTokens, &v.AudioInputTokens, &v.AudioOutputTokens, &v.CacheTokens, &v.CacheWriteTokens, &v.CacheWrite5mTokens, &v.CacheWrite1hTokens, &v.Quota, &v.Amount, &v.BeforeAmount, &v.SettlementDiscount, &v.UpdatedAt, &prices); err != nil {
			return nil, err
		}
		v.UnitPrices, err = billing.ParseUnitPriceGroups(prices)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
