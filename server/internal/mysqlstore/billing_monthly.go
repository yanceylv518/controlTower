package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// Monthly bills copy only completed daily aggregates. No source reader or
// request detail spool is involved, and later daily deletion cannot rewrite them.
func (s Store) createBillingMonthFromDays(ctx context.Context, tx *sql.Tx, job billing.Job, name string) error {
	job.MoneySnapshot = nil
	subject := job.UserID
	if job.JobType == "upstream_statement" {
		subject = job.UpstreamID
	}
	rows, err := tx.QueryContext(ctx, `SELECT j.id,j.range_from,j.range_to,j.status,j.data_source,COALESCE(m.snapshot_json,'') FROM billing_jobs j JOIN billing_statement_jobs st ON st.job_id=j.id LEFT JOIN billing_job_money_snapshots b ON b.job_id=j.id LEFT JOIN billing_money_snapshots m ON m.id=b.snapshot_id WHERE j.instance_id=? AND st.statement_type=? AND st.subject_id=? AND j.usage_version>=3 AND j.bill_period='daily' AND j.status IN ('complete','no_data') AND j.range_from>=? AND j.range_to<=? `+billingCurrentGenerationSQL+` ORDER BY j.created_at DESC,j.id`, job.InstanceID, job.JobType, subject, job.From.UTC(), job.To.UTC())
	if err != nil {
		return err
	}
	covered := map[string]bool{}
	ids := []string{}
	for rows.Next() {
		var id, status, source, raw string
		var from, to time.Time
		if err = rows.Scan(&id, &from, &to, &status, &source, &raw); err != nil {
			rows.Close()
			return err
		}
		if !from.Equal(billing.CompleteDayBoundary(from)) || !to.Equal(from.AddDate(0, 0, 1)) || to.After(billing.CompleteDayBoundary(job.CreatedAt)) {
			continue
		}
		key := from.In(billing.BusinessLocation).Format("2006-01-02")
		if _, exists := covered[key]; exists {
			continue
		}
		covered[key] = status == "no_data"
		if status == "no_data" {
			continue
		}
		var money billing.MoneySnapshot
		if err = json.Unmarshal([]byte(raw), &money); err != nil {
			rows.Close()
			return err
		}
		if job.MoneySnapshot == nil {
			job.MoneySnapshot = &money
			job.DataSource = source
		} else {
			a, ar, e := billing.SettlementDisplay(job.MoneySnapshot)
			if e != nil {
				rows.Close()
				return e
			}
			b, br, e := billing.SettlementDisplay(&money)
			if e != nil {
				rows.Close()
				return e
			}
			if a.Type != b.Type || a.Symbol != b.Symbol || ar.Cmp(br) != 0 {
				rows.Close()
				return billing.ErrDailyCurrencyMismatch
			}
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return billing.ErrStatementNoData
	}
	job.Status = "complete"
	job.TotalSteps = 0
	job.CompletedSteps = 0
	if err = createBillingJobTx(ctx, tx, job, nil); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO billing_statement_jobs(job_id,statement_type,subject_id,subject_name,created_at) VALUES(?,?,?,?,?)`, job.ID, job.JobType, subject, name, job.CreatedAt); err != nil {
		return err
	}
	const columns = "instance_id,bill_day,user_id,username,token_id,token_name,channel_id,channel_name,model_name,request_count,prompt_tokens,completion_tokens,cache_read_tokens,cache_write_tokens,cache_write_5m_tokens,cache_write_1h_tokens,calculated_quota,total_amount,updated_at,image_input_tokens,image_output_tokens,audio_input_tokens,audio_output_tokens,empty_output_count,empty_output_amount,before_amount,before_known_count,settlement_discount,unit_prices"
	args := []any{job.ID}
	for _, id := range ids {
		args = append(args, id)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO billing_compact_daily_totals(job_id,`+columns+`) SELECT ?,`+columns+` FROM billing_compact_daily_totals WHERE job_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, args...); err != nil {
		return err
	}
	// Retain explicit lineage without retaining original orders.
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `INSERT INTO billing_month_daily_sources(month_job_id,daily_job_id) VALUES(?,?)`, job.ID, id); err != nil {
			return err
		}
	}
	if err = recordStandaloneBillingTask(ctx, tx, job); err != nil {
		return err
	}
	if err = snapshotBillingMonthCoverage(ctx, tx, job, covered); err != nil {
		return err
	}
	return s.finalizeBillingStatement(ctx, tx, job, time.Now().UTC())
}
