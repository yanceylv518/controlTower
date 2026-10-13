package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"encoding/json"
	"time"
)

func (s Store) PutBillingTierStatistics(ctx context.Context, jobID string, day time.Time, userID int64, rows []billing.TierStatistics) error {
	raw, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO billing_tier_statistics(job_id,bill_day,user_id,statistics_json) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE statistics_json=VALUES(statistics_json)`, jobID, billingCalendarDate(day), userID, string(raw))
	return err
}
func (s Store) QueryBillingTierStatistics(ctx context.Context, jobID string) ([]billing.TierStatistics, error) {
	rows, err := s.QueryBillingDailyTierStatistics(ctx, jobID)
	if err != nil {
		return nil, err
	}
	out := []billing.TierStatistics{}
	for _, row := range rows {
		out = append(out, row.TierStatistics)
	}
	return out, nil
}

func (s Store) QueryBillingDailyTierStatistics(ctx context.Context, jobID string) ([]billing.DailyTierStatistics, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT bill_day,statistics_json FROM billing_tier_statistics WHERE job_id=? ORDER BY bill_day,user_id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []billing.DailyTierStatistics{}
	for rows.Next() {
		var raw string
		var day time.Time
		if err = rows.Scan(&day, &raw); err != nil {
			return nil, err
		}
		var values []billing.TierStatistics
		if err = json.Unmarshal([]byte(raw), &values); err != nil {
			return nil, err
		}
		for _, value := range values {
			out = append(out, billing.DailyTierStatistics{Day: day.Format("2006-01-02"), TierStatistics: value})
		}
	}
	return out, rows.Err()
}
