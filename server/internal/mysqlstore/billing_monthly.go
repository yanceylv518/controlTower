package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"encoding/json"
	"fmt"
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
	job.Status = "pending"
	job.TotalSteps = 0
	job.CompletedSteps = 0
	if err = createBillingJobTx(ctx, tx, job, nil); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO billing_statement_jobs(job_id,statement_type,subject_id,subject_name,created_at) VALUES(?,?,?,?,?)`, job.ID, job.JobType, subject, name, job.CreatedAt); err != nil {
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
	return nil
}

// CompleteBillingMonth runs under the same site lease as day generation and
// reports. Enqueue freezes lineage/currency; publication copies those exact
// daily snapshots and preserves the previous bill if anything fails.
func (s Store) CompleteBillingMonth(ctx context.Context, job billing.Job) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM billing_jobs WHERE id=? AND instance_id=? FOR UPDATE`, job.ID, job.InstanceID).Scan(&status); err != nil {
		return err
	}
	if status != "publishing" {
		return billing.ErrGenerationCancelled
	}
	rows, err := tx.QueryContext(ctx, `SELECT src.daily_job_id,j.id FROM billing_month_daily_sources src LEFT JOIN billing_jobs j ON j.id=src.daily_job_id WHERE src.month_job_id=?`, job.ID)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		var exists sql.NullString
		if err = rows.Scan(&id, &exists); err != nil {
			rows.Close()
			return err
		}
		if !exists.Valid {
			rows.Close()
			return fmt.Errorf("monthly daily snapshot missing")
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return fmt.Errorf("monthly daily snapshots missing")
	}
	current, err := billingMonthSourcesCurrent(ctx, tx, job, ids)
	if err != nil {
		return err
	}
	if !current {
		// Enqueue can read a snapshot established before a concurrently
		// completed day. Its invalidation ran before this month was inserted,
		// so recheck under the site's execution lease before publishing it.
		if _, err = tx.ExecContext(ctx, `UPDATE billing_jobs SET status='superseded',updated_at=UTC_TIMESTAMP(6) WHERE id=? AND status='publishing'`, job.ID); err != nil {
			return err
		}
		return tx.Commit()
	}
	const columns = "instance_id,bill_day,user_id,username,token_id,token_name,channel_id,channel_name,model_name,request_count,prompt_tokens,completion_tokens,cache_read_tokens,cache_write_tokens,cache_write_5m_tokens,cache_write_1h_tokens,calculated_quota,total_amount,updated_at,image_input_tokens,image_output_tokens,audio_input_tokens,audio_output_tokens,empty_output_count,empty_output_amount,before_amount,before_known_count,settlement_discount,unit_prices"
	args := []any{job.ID}
	for _, id := range ids {
		args = append(args, id)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO billing_compact_daily_totals(job_id,`+columns+`) SELECT ?,`+columns+` FROM billing_compact_daily_totals WHERE job_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, args...); err != nil {
		return err
	}

	if err = s.finalizeBillingStatement(ctx, tx, job, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// The coordinator prevents daily publication while this check and the monthly
// copy run. Check both nonempty source IDs and coverage added by no-data days.
func billingMonthSourcesCurrent(ctx context.Context, tx *sql.Tx, job billing.Job, ids []string) (bool, error) {
	var subject int64
	if err := tx.QueryRowContext(ctx, `SELECT subject_id FROM billing_statement_jobs WHERE job_id=?`, job.ID).Scan(&subject); err != nil {
		return false, err
	}
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT coverage_json FROM billing_month_coverage WHERE job_id=?`, job.ID).Scan(&raw); err != nil {
		return false, err
	}
	var coverage billing.MonthlyCoverage
	if err := json.Unmarshal([]byte(raw), &coverage); err != nil {
		return false, err
	}
	covered := func(day string) bool {
		for _, span := range coverage.Ranges {
			if day >= span.From && day <= span.To {
				return true
			}
		}
		return false
	}
	rows, err := tx.QueryContext(ctx, `SELECT j.id,j.range_from,j.range_to,j.status FROM billing_jobs j JOIN billing_statement_jobs st ON st.job_id=j.id WHERE j.instance_id=? AND st.statement_type=? AND st.subject_id=? AND j.usage_version>=3 AND j.bill_period='daily' AND j.status IN ('complete','no_data') AND j.range_from>=? AND j.range_to<=? `+billingCurrentGenerationSQL+` ORDER BY j.created_at DESC,j.id`, job.InstanceID, job.JobType, subject, job.From.UTC(), job.To.UTC())
	if err != nil {
		return false, err
	}
	defer rows.Close()
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	seen := map[string]bool{}
	current := true
	boundary := billing.CompleteDayBoundary(time.Now())
	for rows.Next() {
		var id, status string
		var from, to time.Time
		if err = rows.Scan(&id, &from, &to, &status); err != nil {
			return false, err
		}
		if !from.Equal(billing.CompleteDayBoundary(from)) || !to.Equal(from.AddDate(0, 0, 1)) || to.After(boundary) {
			continue
		}
		day := from.In(billing.BusinessLocation).Format("2006-01-02")
		if seen[day] {
			continue
		}
		seen[day] = true
		if !covered(day) {
			current = false
		}
		if status == "complete" {
			if !want[id] {
				current = false
			}
			delete(want, id)
		}
	}
	return current && len(want) == 0, rows.Err()
}
