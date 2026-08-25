package mysqlstore

import (
	"context"
	"database/sql"
	"time"

	"controltower/server/internal/billing"
)

func (s Store) ListBillingDiscountRules(ctx context.Context, site, kind string) ([]billing.DiscountRule, error) {
	q := `SELECT r.id,r.instance_id,r.discount_type,r.subject_id,COALESCE(u.name,''),r.channel_id,COALESCE(b.channel_name,''),r.model_name,CAST(r.discount AS CHAR),r.effective_from,r.effective_to,r.remark,r.created_at,r.updated_at,r.updated_by FROM billing_discount_rules r LEFT JOIN billing_upstreams u ON r.discount_type='upstream_channel' AND u.id=r.subject_id AND u.instance_id=r.instance_id LEFT JOIN billing_upstream_channel_bindings b ON b.instance_id=r.instance_id AND b.upstream_id=r.subject_id AND b.channel_id=r.channel_id WHERE r.instance_id=?`
	args := []any{site}
	if kind != "" {
		q += ` AND r.discount_type=?`
		args = append(args, kind)
	}
	q += ` ORDER BY r.discount_type,r.subject_id,r.channel_id,r.model_name,r.effective_from`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.DiscountRule{}
	for rows.Next() {
		var v billing.DiscountRule
		var end sql.NullTime
		if err = rows.Scan(&v.ID, &v.InstanceID, &v.DiscountType, &v.SubjectID, &v.SubjectName, &v.ChannelID, &v.ChannelName, &v.ModelName, &v.Discount, &v.EffectiveFrom, &end, &v.Remark, &v.CreatedAt, &v.UpdatedAt, &v.UpdatedBy); err != nil {
			return nil, err
		}
		if end.Valid {
			v.EffectiveTo = &end.Time
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func (s Store) PutBillingDiscountRule(ctx context.Context, v billing.DiscountRule) (billing.DiscountRule, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	fromDay := v.EffectiveFrom.In(billing.BusinessLocation).Format("2006-01-02")
	var endDay any
	if v.EffectiveTo != nil {
		endDay = v.EffectiveTo.In(billing.BusinessLocation).Format("2006-01-02")
	}
	if v.DiscountType == billing.DiscountUpstreamChannel {
		var exists int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_upstream_channel_bindings WHERE instance_id=? AND upstream_id=? AND channel_id=?`, v.InstanceID, v.SubjectID, v.ChannelID).Scan(&exists); err != nil || exists != 1 {
			return v, sql.ErrNoRows
		}
	}
	var overlaps int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_discount_rules WHERE instance_id=? AND discount_type=? AND subject_id=? AND channel_id=? AND model_name=? AND id<>? AND effective_from<COALESCE(?,DATE('9999-12-31')) AND COALESCE(effective_to,DATE('9999-12-31'))>? FOR UPDATE`, v.InstanceID, v.DiscountType, v.SubjectID, v.ChannelID, v.ModelName, v.ID, endDay, fromDay).Scan(&overlaps); err != nil {
		return v, err
	}
	if overlaps > 0 {
		return v, billing.ErrDiscountOverlap
	}
	now := time.Now().UTC()
	if v.ID == 0 {
		result, e := tx.ExecContext(ctx, `INSERT INTO billing_discount_rules(instance_id,discount_type,subject_id,channel_id,model_name,discount,effective_from,effective_to,remark,created_at,updated_at,updated_by) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, v.InstanceID, v.DiscountType, v.SubjectID, v.ChannelID, v.ModelName, v.Discount, fromDay, endDay, v.Remark, now, now, v.UpdatedBy)
		if e != nil {
			return v, e
		}
		v.ID, err = result.LastInsertId()
		v.CreatedAt = now
	} else {
		result, e := tx.ExecContext(ctx, `UPDATE billing_discount_rules SET discount=?,effective_from=?,effective_to=?,remark=?,updated_at=?,updated_by=? WHERE id=? AND instance_id=?`, v.Discount, fromDay, endDay, v.Remark, now, v.UpdatedBy, v.ID, v.InstanceID)
		if e != nil {
			return v, e
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return v, sql.ErrNoRows
		}
	}
	if err != nil {
		return v, err
	}
	v.UpdatedAt = now
	return v, tx.Commit()
}

func (s Store) DeleteBillingDiscountRule(ctx context.Context, site string, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM billing_discount_rules WHERE instance_id=? AND id=?`, site, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s Store) ListBillingStatementDiscounts(ctx context.Context, jobID string) ([]billing.StatementDiscount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT discount_type,subject_id,channel_id,channel_name,model_name,CAST(discount AS CHAR),effective_from,effective_to,source_rule_id FROM billing_statement_discount_snapshots WHERE job_id=? ORDER BY effective_from,id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.StatementDiscount{}
	for rows.Next() {
		var v billing.StatementDiscount
		var end sql.NullTime
		if err = rows.Scan(&v.DiscountType, &v.SubjectID, &v.ChannelID, &v.ChannelName, &v.ModelName, &v.Discount, &v.EffectiveFrom, &end, &v.SourceRuleID); err != nil {
			return nil, err
		}
		if end.Valid {
			v.EffectiveTo = &end.Time
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func (s Store) QueryBillingStatementAggregates(ctx context.Context, jobID string) ([]billing.StatementAggregateRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id,user_id,MAX(username),channel_id,MAX(channel_name),model_name,bill_day,SUM(request_count),SUM(prompt_tokens),SUM(completion_tokens),SUM(cache_read_tokens),SUM(cache_write_tokens),SUM(cache_write_5m_tokens),SUM(cache_write_1h_tokens),SUM(calculated_quota),CAST(SUM(total_amount) AS CHAR) FROM billing_compact_daily_totals WHERE job_id=? GROUP BY instance_id,user_id,channel_id,model_name,bill_day ORDER BY bill_day,channel_id,model_name`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.StatementAggregateRow{}
	for rows.Next() {
		var v billing.StatementAggregateRow
		if err = rows.Scan(&v.InstanceID, &v.UserID, &v.Username, &v.ChannelID, &v.ChannelName, &v.ModelName, &v.Day, &v.RequestCount, &v.PromptTokens, &v.CompletionTokens, &v.CacheTokens, &v.CacheWriteTokens, &v.CacheWrite5mTokens, &v.CacheWrite1hTokens, &v.Quota, &v.Amount); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
