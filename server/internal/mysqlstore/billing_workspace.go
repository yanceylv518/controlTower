package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"time"
)

func (s Store) BillingWorkspace(ctx context.Context, site, kind string, id int64, from, to time.Time) ([]billing.WorkspaceBill, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT j.id FROM billing_jobs j JOIN billing_statement_jobs st ON st.job_id=j.id WHERE j.instance_id=? AND j.job_type=? AND st.subject_id=? AND j.usage_version>=3 AND j.range_from<? AND j.range_to>? ORDER BY j.created_at DESC LIMIT 500`, site, kind, id, to.UTC(), from.UTC())
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []billing.WorkspaceBill{}
	for _, id := range ids {
		v := billing.WorkspaceBill{}
		v.Job, err = s.BillingJob(ctx, id)
		if err != nil {
			return nil, err
		}
		err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(request_count),0),COALESCE(SUM(prompt_tokens),0),COALESCE(SUM(completion_tokens),0),COALESCE(SUM(cache_read_tokens),0),COALESCE(SUM(cache_write_tokens),0),CAST(COALESCE(SUM(total_amount),0) AS CHAR),CASE WHEN SUM(CASE WHEN before_known_count=request_count OR settlement_discount IN ('','1','1.000000') THEN 0 ELSE 1 END)=0 THEN CAST(SUM(CASE WHEN before_known_count=request_count THEN before_amount ELSE total_amount END) AS CHAR) ELSE '' END,CASE WHEN COUNT(*)=0 THEN '' WHEN MIN(COALESCE(NULLIF(settlement_discount,''),'1.000000'))=MAX(COALESCE(NULLIF(settlement_discount,''),'1.000000')) THEN MIN(COALESCE(NULLIF(settlement_discount,''),'1.000000')) ELSE 'mixed' END,COALESCE(SUM(empty_output_count),0),CAST(COALESCE(SUM(empty_output_amount),0) AS CHAR),COALESCE(SUM(image_input_tokens),0),COALESCE(SUM(image_output_tokens),0),COALESCE(SUM(audio_input_tokens),0),COALESCE(SUM(audio_output_tokens),0) FROM billing_compact_daily_totals WHERE job_id=?`, id).Scan(&v.Requests, &v.Input, &v.Output, &v.CacheRead, &v.CacheWrite, &v.Amount, &v.BeforeAmount, &v.Discount, &v.EmptyCount, &v.EmptyAmount, &v.ImageInputTokens, &v.ImageOutputTokens, &v.AudioInputTokens, &v.AudioOutputTokens)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
