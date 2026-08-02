package mysqlstore

import (
	"context"
	"database/sql"
	"time"

	"controltower/server/internal/billing"
)

func (s Store) ListBillingSites(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT COALESCE(NULLIF(site_id,''),id) FROM instances WHERE enabled=1 AND logs_readonly_dsn<>'' ORDER BY COALESCE(NULLIF(site_id,''),id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sites []string
	for rows.Next() {
		var site string
		if err = rows.Scan(&site); err != nil {
			return nil, err
		}
		sites = append(sites, site)
	}
	return sites, rows.Err()
}

func (s Store) ListBillingPrices(ctx context.Context, instanceID string) ([]billing.PriceRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id,model_name,effective_from,tier_from,input_price,output_price,cache_price,updated_at,updated_by FROM billing_prices WHERE instance_id=? ORDER BY model_name,effective_from DESC,tier_from`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []billing.PriceRecord
	for rows.Next() {
		var v billing.PriceRecord
		if err = rows.Scan(&v.InstanceID, &v.ModelName, &v.EffectiveFrom, &v.TierFrom, &v.Input, &v.Output, &v.Cache, &v.UpdatedAt, &v.UpdatedBy); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// PutBillingPriceSchedule writes a complete effective schedule atomically.
// Historical effective dates remain untouched.
func (s Store) PutBillingPriceSchedule(ctx context.Context, records []billing.PriceRecord) error {
	if len(records) == 0 {
		return nil
	}
	if err := billing.ValidateTierSchedule(priceValues(records)); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	first := records[0]
	if _, err = tx.ExecContext(ctx, `DELETE FROM billing_prices WHERE instance_id=? AND model_name=? AND effective_from=?`, first.InstanceID, first.ModelName, first.EffectiveFrom); err != nil {
		return err
	}
	for _, v := range records {
		if v.InstanceID != first.InstanceID || v.ModelName != first.ModelName || !v.EffectiveFrom.Equal(first.EffectiveFrom) {
			return sql.ErrNoRows
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO billing_prices(instance_id,model_name,effective_from,tier_from,input_price,output_price,cache_price,updated_at,updated_by) VALUES(?,?,?,?,?,?,?,?,?)`, v.InstanceID, v.ModelName, v.EffectiveFrom, v.TierFrom, v.Input, v.Output, v.Cache, v.UpdatedAt, v.UpdatedBy); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func priceValues(records []billing.PriceRecord) []billing.Price {
	out := make([]billing.Price, len(records))
	for i := range records {
		out[i] = records[i].Price
	}
	return out
}

func (s Store) ListBillingGroupRatios(ctx context.Context, instanceID string) ([]billing.GroupRatio, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id,group_name,ratio,updated_at,updated_by FROM billing_group_ratios WHERE instance_id=? ORDER BY group_name`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []billing.GroupRatio
	for rows.Next() {
		var v billing.GroupRatio
		if err = rows.Scan(&v.InstanceID, &v.GroupName, &v.Ratio, &v.UpdatedAt, &v.UpdatedBy); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s Store) PutBillingGroupRatio(ctx context.Context, v billing.GroupRatio) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO billing_group_ratios(instance_id,group_name,ratio,updated_at,updated_by) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE ratio=VALUES(ratio),updated_at=VALUES(updated_at),updated_by=VALUES(updated_by)`, v.InstanceID, v.GroupName, v.Ratio, v.UpdatedAt, v.UpdatedBy)
	return err
}

func (s Store) ReplaceBillingDay(ctx context.Context, instanceID string, day time.Time, rows []billing.DailyRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM billing_daily WHERE instance_id=? AND day=?`, instanceID, day); err != nil {
		return err
	}
	for _, v := range rows {
		if v.InstanceID != instanceID || !sameDate(v.Day, day) {
			return sql.ErrNoRows
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO billing_daily(instance_id,user_id,username,model_name,group_name,tier_from,day,request_count,prompt_tokens,completion_tokens,cache_tokens,quota,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.InstanceID, v.UserID, v.Username, v.ModelName, v.GroupName, v.TierFrom, v.Day, v.RequestCount, v.PromptTokens, v.CompletionTokens, v.CacheTokens, v.Quota, v.UpdatedAt)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s Store) PutBillingRatioSnapshot(ctx context.Context, instanceID string, day time.Time, raw string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO billing_ratio_snapshot(instance_id,day,ratios_json) VALUES(?,?,?) ON DUPLICATE KEY UPDATE ratios_json=VALUES(ratios_json)`, instanceID, day, raw)
	return err
}

func (s Store) PutBillingBalanceSnapshots(ctx context.Context, instanceID string, day time.Time, balances map[int64]int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM billing_balance_snapshot WHERE instance_id=? AND day=?`, instanceID, day); err != nil {
		return err
	}
	for userID, balance := range balances {
		if _, err = tx.ExecContext(ctx, `INSERT INTO billing_balance_snapshot(instance_id,user_id,day,balance) VALUES(?,?,?,?)`, instanceID, userID, day, balance); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func sameDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
