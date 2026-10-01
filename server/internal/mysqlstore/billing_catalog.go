package mysqlstore

import (
	"context"
	"strings"
	"time"

	"controltower/server/internal/billing"
)

// Old v3 rows may predate bill_period. Match the workspace's Shanghai calendar rules.
const catalogPeriodSQL = `CASE WHEN j.bill_period<>'' THEN j.bill_period WHEN TIME(DATE_ADD(j.range_from, INTERVAL 8 HOUR))='00:00:00' AND TIME(DATE_ADD(j.range_to, INTERVAL 8 HOUR))='00:00:00' AND TIMESTAMPDIFF(SECOND,j.range_from,j.range_to)=86400 THEN 'daily' WHEN TIME(DATE_ADD(j.range_from, INTERVAL 8 HOUR))='00:00:00' AND TIME(DATE_ADD(j.range_to, INTERVAL 8 HOUR))='00:00:00' AND DAY(DATE_ADD(j.range_from, INTERVAL 8 HOUR))=1 AND DAY(DATE_ADD(j.range_to, INTERVAL 8 HOUR))=1 THEN 'monthly' ELSE 'temporary' END`

func catalogWhere(f billing.CatalogFilter) (string, []any) {
	where := ` FROM billing_jobs j JOIN billing_statement_jobs st ON st.job_id=j.id WHERE j.instance_id=? AND j.job_type=? AND st.statement_type=j.job_type AND j.usage_version>=3 AND j.status='complete'`
	args := []any{f.Site, f.Kind}
	if f.Month != "" {
		from, _ := time.ParseInLocation("2006-01", f.Month, billing.BusinessLocation)
		where += ` AND j.range_from<? AND j.range_to>?`
		args = append(args, from.AddDate(0, 1, 0).UTC(), from.UTC())
	}
	if f.Query != "" {
		// ! escaping makes literal %, _ and backslashes safe in LIKE, without relying on SQL mode.
		like := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(f.Query) + "%"
		name := `COALESCE(NULLIF(st.subject_name,''),CASE WHEN st.statement_type='user_statement' THEN (SELECT MAX(c.username) FROM billing_compact_daily_totals c WHERE c.job_id=j.id AND c.user_id=st.subject_id) ELSE '' END,'')`
		billNo := `CONCAT(COALESCE(NULLIF(` + name + `,''),CONCAT('对象',st.subject_id)),'-',DATE_FORMAT(DATE_ADD(j.range_from,INTERVAL 8 HOUR),'%Y%m%d'),'至',DATE_FORMAT(DATE_ADD(DATE_SUB(j.range_to,INTERVAL 1 MICROSECOND),INTERVAL 8 HOUR),'%Y%m%d'),'-',DATE_FORMAT(DATE_ADD(j.created_at,INTERVAL 8 HOUR),'%Y%m%d%H%i%s'))`
		where += ` AND (` + name + ` LIKE ? ESCAPE '!' OR CAST(st.subject_id AS CHAR)=? OR j.id LIKE ? ESCAPE '!' OR ` + billNo + ` LIKE ? ESCAPE '!')`
		args = append(args, like, strings.TrimPrefix(f.Query, "#"), like, like)
	}
	return where, args
}

func (s Store) BillingCatalog(ctx context.Context, f billing.CatalogFilter) (billing.CatalogPage, error) {
	out := billing.CatalogPage{Items: []billing.CatalogBill{}, Counts: map[string]int64{"daily": 0, "monthly": 0, "temporary": 0}, Page: f.Page, PageSize: f.PageSize}
	where, args := catalogWhere(f)
	rows, err := s.db.QueryContext(ctx, `SELECT `+catalogPeriodSQL+`,COUNT(*),COUNT(DISTINCT st.subject_id)`+where+` GROUP BY `+catalogPeriodSQL, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var period string
		var count, subjects int64
		if err = rows.Scan(&period, &count, &subjects); err != nil {
			rows.Close()
			return out, err
		}
		out.Counts[period] = count
		if period == f.Period {
			out.Total = count
			out.Subjects = subjects
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	where += ` AND ` + catalogPeriodSQL + `=? ORDER BY COALESCE(j.finished_at,j.updated_at) DESC,j.id DESC LIMIT ? OFFSET ?`
	args = append(args, f.Period, f.PageSize, (f.Page-1)*f.PageSize)
	rows, err = s.db.QueryContext(ctx, `SELECT j.id,COALESCE(j.finished_at,j.updated_at)`+where, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v billing.CatalogBill
		if err = rows.Scan(&v.Job.ID, &v.GeneratedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// Only enrich the requested page, reusing the immutable name/currency/coverage snapshots.
	for i := range out.Items {
		v := &out.Items[i]
		v.Job, err = s.BillingJob(ctx, v.Job.ID)
		if err != nil {
			return out, err
		}
		if v.Job.BillPeriod == "" {
			v.Job.BillPeriod = f.Period
		}
		err = s.db.QueryRowContext(ctx, `SELECT `+workspaceTotalsSQL+` FROM billing_compact_daily_totals WHERE job_id=?`, v.Job.ID).Scan(&v.Requests, &v.Input, &v.Output, &v.CacheRead, &v.CacheWrite, &v.Amount, &v.BeforeAmount, &v.Discount, &v.EmptyCount, &v.EmptyAmount, &v.ImageInputTokens, &v.ImageOutputTokens, &v.AudioInputTokens, &v.AudioOutputTokens)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}
