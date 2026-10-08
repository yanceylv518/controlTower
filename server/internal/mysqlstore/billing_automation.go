package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"time"
)

func (s Store) PutBillingAutomaticTarget(ctx context.Context, t billing.AutomaticTarget) error {
	if !t.To.IsZero() {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO billing_generation_ranges(instance_id,kind,subject_id,range_from,range_to,created_at,exclude_zero_output) VALUES(?,?,?,?,?,UTC_TIMESTAMP(6),?)`, t.InstanceID, t.Kind, t.SubjectID, t.From.In(billing.BusinessLocation).Format("2006-01-02"), t.To.In(billing.BusinessLocation).Format("2006-01-02"), t.ExcludeZeroOutput); err != nil {
			return err
		}
		// Future daily automation starts today; selecting a historical month does not
		// authorize scanning every intervening month.
		if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO billing_automatic_targets(instance_id,kind,subject_id,start_day,created_at) VALUES(?,?,?,?,UTC_TIMESTAMP(6))`, t.InstanceID, t.Kind, t.SubjectID, billing.CompleteDayBoundary(time.Now()).Format("2006-01-02")); err != nil {
			return err
		}
		return tx.Commit()
	}

	_, err := s.db.ExecContext(ctx, `INSERT INTO billing_automatic_targets(instance_id,kind,subject_id,start_day,created_at) VALUES(?,?,?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE start_day=LEAST(start_day,VALUES(start_day))`, t.InstanceID, t.Kind, t.SubjectID, t.From.In(billing.BusinessLocation).Format("2006-01-02"))
	return err
}
func (s Store) ListBillingAutomaticTargets(ctx context.Context) ([]billing.AutomaticTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.instance_id,r.kind,r.subject_id,r.range_from,r.range_to,(SELECT t.work_until FROM billing_generation_tasks t WHERE t.id=r.batch_id),r.exclude_zero_output FROM billing_generation_ranges r WHERE r.cancelled=0 UNION ALL SELECT instance_id COLLATE utf8mb4_unicode_ci,kind COLLATE utf8mb4_unicode_ci,subject_id,start_day,DATE('9999-12-31'),NULL,(kind='upstream_statement') FROM billing_automatic_targets a WHERE a.kind<>'upstream_statement' OR EXISTS (SELECT 1 FROM billing_upstreams u WHERE u.instance_id COLLATE utf8mb4_unicode_ci=a.instance_id COLLATE utf8mb4_unicode_ci AND u.id=a.subject_id AND u.enabled=1)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []billing.AutomaticTarget{}
	for rows.Next() {
		var t billing.AutomaticTarget
		var d, end time.Time
		var workUntil sql.NullTime
		if err = rows.Scan(&t.InstanceID, &t.Kind, &t.SubjectID, &d, &end, &workUntil, &t.ExcludeZeroOutput); err != nil {
			return nil, err
		}
		t.From, _ = time.ParseInLocation("2006-01-02", d.Format("2006-01-02"), billing.BusinessLocation)
		if end.Year() != 9999 {
			t.To, _ = time.ParseInLocation("2006-01-02", end.Format("2006-01-02"), billing.BusinessLocation)
		}
		if workUntil.Valid {
			t.ProgressUntil = workUntil.Time
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s Store) MissingBillingDays(ctx context.Context, t billing.AutomaticTarget, to time.Time) ([]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT j.range_from,j.range_to FROM billing_jobs j JOIN billing_statement_jobs s ON s.job_id=j.id WHERE j.instance_id=? AND s.statement_type=? AND s.subject_id=? AND j.usage_version>=3 AND j.bill_period='daily' AND (j.status IN ('pending','running','publishing','complete','no_data') OR (j.status='failed' AND (j.error_message='cancelled manually' OR j.updated_at>DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 HOUR)))) AND j.range_from>=? AND j.range_to<=? `+billingCurrentGenerationSQL, t.InstanceID, t.Kind, t.SubjectID, t.From.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	covered := map[int64]bool{}
	for rows.Next() {
		var from, end time.Time
		if err = rows.Scan(&from, &end); err != nil {
			return nil, err
		}
		if from.Equal(billing.CompleteDayBoundary(from)) && end.Equal(from.AddDate(0, 0, 1)) {
			covered[from.Unix()] = true
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	checks, e := s.db.QueryContext(ctx, `SELECT bill_day FROM billing_day_checks WHERE instance_id=? AND kind=? AND subject_id=? AND has_consumption=0 AND bill_day>=? AND bill_day<?`, t.InstanceID, t.Kind, t.SubjectID, t.From.In(billing.BusinessLocation).Format("2006-01-02"), to.In(billing.BusinessLocation).Format("2006-01-02"))
	if e != nil {
		return nil, e
	}
	for checks.Next() {
		var date time.Time
		if e = checks.Scan(&date); e != nil {
			checks.Close()
			return nil, e
		}
		local, _ := time.ParseInLocation("2006-01-02", date.Format("2006-01-02"), billing.BusinessLocation)
		covered[local.Unix()] = true
	}
	e = checks.Err()
	checks.Close()
	if e != nil {
		return nil, e
	}
	out := []time.Time{}
	for day := t.From; day.Before(to); day = day.AddDate(0, 0, 1) {
		if !covered[day.Unix()] {
			out = append(out, day)
		}
	}
	return out, nil
}

func (s Store) BillingRemainingDays(ctx context.Context, site, kind string, id int64, from, to time.Time) (int, error) {
	if end := billing.CompleteDayBoundary(time.Now()); to.After(end) {
		to = end
	}
	days, err := s.MissingBillingDays(ctx, billing.AutomaticTarget{InstanceID: site, Kind: kind, SubjectID: id, From: from}, to)
	if err != nil {
		return 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT bill_day FROM billing_activity_days WHERE instance_id=? AND kind=? AND subject_id=?`, site, kind, id)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	active := map[string]bool{}
	for rows.Next() {
		var d time.Time
		if err = rows.Scan(&d); err != nil {
			return 0, err
		}
		active[d.Format("2006-01-02")] = true
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	count := 0
	for _, day := range days {
		if active[day.In(billing.BusinessLocation).Format("2006-01-02")] {
			count++
		}
	}
	return count, nil
}

// Include open months; publication copies only the completed daily snapshots.
func (s Store) MissingBillingMonths(ctx context.Context, t billing.AutomaticTarget, to time.Time) ([]time.Time, error) {
	local := t.From.In(billing.BusinessLocation)
	first := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, billing.BusinessLocation)
	rows, err := s.db.QueryContext(ctx, `SELECT j.range_from,j.range_to FROM billing_jobs j JOIN billing_statement_jobs st ON st.job_id=j.id WHERE j.instance_id=? AND st.statement_type=? AND st.subject_id=? AND j.usage_version>=3 AND j.bill_period='monthly' AND (j.status IN ('pending','running','publishing','complete','no_data') OR (j.status='failed' AND (j.error_message='cancelled manually' OR j.updated_at>DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 HOUR)))) AND j.range_from>=? AND j.range_to<=? `+billingCurrentGenerationSQL, t.InstanceID, t.Kind, t.SubjectID, first.UTC(), to.In(billing.BusinessLocation).AddDate(0, 1, 0).UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	covered := map[int64]bool{}
	for rows.Next() {
		var from, end time.Time
		if err = rows.Scan(&from, &end); err != nil {
			return nil, err
		}
		if end.Equal(from.In(billing.BusinessLocation).AddDate(0, 1, 0)) {
			covered[from.Unix()] = true
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	result := []time.Time{}
	for month := first; month.Before(to); month = month.AddDate(0, 1, 0) {
		if !covered[month.Unix()] {
			result = append(result, month)
		}
	}
	return result, nil
}

func (s Store) BillingDayActivity(ctx context.Context, t billing.AutomaticTarget) (bool, bool, error) {
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT has_consumption FROM billing_day_checks WHERE instance_id=? AND kind=? AND subject_id=? AND bill_day=?`, t.InstanceID, t.Kind, t.SubjectID, t.From.In(billing.BusinessLocation).Format("2006-01-02")).Scan(&active)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	return active, err == nil, err
}
func (s Store) RecordBillingDayActivity(ctx context.Context, t billing.AutomaticTarget, active bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	day := t.From.In(billing.BusinessLocation).Format("2006-01-02")
	if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO billing_day_checks(instance_id,kind,subject_id,bill_day,has_consumption,checked_at) VALUES(?,?,?,?,?,UTC_TIMESTAMP(6))`, t.InstanceID, t.Kind, t.SubjectID, day, active); err != nil {
		return err
	}
	if !active {
		if _, err = tx.ExecContext(ctx, `UPDATE billing_jobs j JOIN billing_statement_jobs st ON st.job_id=j.id JOIN billing_generation_ranges r ON r.instance_id=j.instance_id AND r.kind=j.job_type AND r.subject_id=st.subject_id SET j.status='superseded',j.updated_at=UTC_TIMESTAMP(6) WHERE j.instance_id=? AND st.subject_id=? AND j.job_type=? AND j.usage_version>=3 AND j.bill_period IN ('daily','monthly') AND j.range_from<=? AND j.range_to>=? AND j.status IN ('complete','no_data') AND r.cancelled=0 AND r.overwrite_existing=1 AND j.created_at<r.generation_started_at AND r.range_from<=? AND r.range_to>?`, t.InstanceID, t.SubjectID, t.Kind, t.From.UTC(), t.To.UTC(), day, day); err != nil {
			return err
		}
	}
	if active {
		if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO billing_activity_days(instance_id,kind,subject_id,bill_day) VALUES(?,?,?,?)`, t.InstanceID, t.Kind, t.SubjectID, day); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Feedback uses only CT task/check state; it never scans the source for a toast.
func (s Store) BillingGenerationFeedback(ctx context.Context, t billing.AutomaticTarget) (string, error) {
	end := billingActivityBoundary(t)
	var active, completed, failed int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(j.status IN ('pending','running','publishing')),0),COALESCE(SUM(j.status='complete'),0),COALESCE(SUM(j.status='failed'),0) FROM billing_jobs j JOIN billing_statement_jobs st ON st.job_id=j.id WHERE j.instance_id=? AND st.statement_type=? AND st.subject_id=? AND j.usage_version>=3 AND j.bill_period='daily' AND j.range_from>=? AND j.range_to<=? `+billingCurrentGenerationSQL, t.InstanceID, t.Kind, t.SubjectID, t.From.UTC(), end.UTC()).Scan(&active, &completed, &failed)
	if err != nil {
		return "", err
	}
	if active > 0 {
		return "generating", nil
	}
	if failed > 0 {
		return "failed", nil
	}
	missing, err := s.MissingBillingDays(ctx, t, end)
	if err != nil {
		return "", err
	}
	if len(missing) > 0 {
		return "registered", nil
	}
	if completed > 0 {
		return "already_complete", nil
	}
	return "no_consumption", nil
}
func billingActivityBoundary(t billing.AutomaticTarget) time.Time {
	end := billing.CompleteDayBoundary(time.Now())
	if !t.To.IsZero() && t.To.Before(end) {
		end = t.To
	}
	return end
}
