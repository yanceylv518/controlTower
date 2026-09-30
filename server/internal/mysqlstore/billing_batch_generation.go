package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

func (s Store) PutBillingAutomaticTargets(ctx context.Context, targets []billing.AutomaticTarget) error {
	if len(targets) == 0 {
		return nil
	}
	release, err := s.BeginBillingGeneration(ctx, targets[0].InstanceID)
	if err != nil {
		return err
	}
	defer release()
	if err = s.freezeBillingTasks(ctx, targets); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw [16]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return err
	}
	batchID := hex.EncodeToString(raw[:])
	if err = insertBillingTask(ctx, tx, batchID, targets); err != nil {
		return err
	}
	for _, t := range targets {
		if _, err = tx.ExecContext(ctx, `INSERT INTO billing_generation_ranges(instance_id,kind,subject_id,range_from,range_to,created_at,overwrite_existing,generation_started_at,batch_id) VALUES(?,?,?,?,?,UTC_TIMESTAMP(6),?,UTC_TIMESTAMP(6),?) ON DUPLICATE KEY UPDATE last_error=NULL,cancelled=0,batch_id=VALUES(batch_id),overwrite_existing=VALUES(overwrite_existing),generation_started_at=VALUES(generation_started_at)`, t.InstanceID, t.Kind, t.SubjectID, t.From.In(billing.BusinessLocation).Format("2006-01-02"), t.To.In(billing.BusinessLocation).Format("2006-01-02"), t.Overwrite, batchID); err != nil {
			return err
		}
		if t.Overwrite {
			if _, err = tx.ExecContext(ctx, `DELETE FROM billing_day_checks WHERE instance_id=? AND kind=? AND subject_id=? AND bill_day>=? AND bill_day<?`, t.InstanceID, t.Kind, t.SubjectID, t.From.In(billing.BusinessLocation).Format("2006-01-02"), t.To.In(billing.BusinessLocation).Format("2006-01-02")); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE billing_jobs j JOIN billing_statement_jobs st ON st.job_id=j.id SET j.updated_at=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 2 HOUR),j.error_message='retry requested' WHERE j.instance_id=? AND j.job_type=? AND st.subject_id=? AND j.status='failed' AND j.range_from>=? AND j.range_to<=?`, t.InstanceID, t.Kind, t.SubjectID, t.From.UTC(), t.To.UTC()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO billing_automatic_targets(instance_id,kind,subject_id,start_day,created_at) VALUES(?,?,?,?,UTC_TIMESTAMP(6))`, t.InstanceID, t.Kind, t.SubjectID, billing.CompleteDayBoundary(time.Now()).Format("2006-01-02")); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s Store) RecordBillingGenerationAttempt(ctx context.Context, t billing.AutomaticTarget, cause error) error {
	if t.To.IsZero() {
		return nil
	}
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE billing_generation_ranges SET last_attempt=UTC_TIMESTAMP(6),last_error=? WHERE instance_id=? AND kind=? AND subject_id=? AND range_from=? AND range_to=?`, message, t.InstanceID, t.Kind, t.SubjectID, t.From.In(billing.BusinessLocation).Format("2006-01-02"), t.To.In(billing.BusinessLocation).Format("2006-01-02"))
	return err
}
func (s Store) BillingGenerationProgress(ctx context.Context, t billing.AutomaticTarget) (billing.GenerationProgress, error) {
	p := billing.GenerationProgress{SubjectID: t.SubjectID, Outcome: "registered", Days: []billing.GenerationDay{}}
	if t.ProgressUntil.IsZero() && !t.To.IsZero() {
		var until time.Time
		err := s.db.QueryRowContext(ctx, `SELECT task.work_until FROM billing_generation_ranges r JOIN billing_generation_tasks task ON task.id=r.batch_id WHERE r.instance_id=? AND r.kind=? AND r.subject_id=? AND r.range_from=? AND r.range_to=?`, t.InstanceID, t.Kind, t.SubjectID, t.From.In(billing.BusinessLocation).Format("2006-01-02"), t.To.In(billing.BusinessLocation).Format("2006-01-02")).Scan(&until)
		if err != nil && err != sql.ErrNoRows {
			return p, err
		}
		if err == nil {
			t.ProgressUntil = until
		}
	}
	end := billingActivityBoundary(t)
	if !t.ProgressUntil.IsZero() && t.ProgressUntil.Before(end) {
		end = t.ProgressUntil
	}
	byDay := map[string]billing.GenerationDay{}
	rows, err := s.db.QueryContext(ctx, `SELECT bill_day,has_consumption FROM billing_day_checks WHERE instance_id=? AND kind=? AND subject_id=? AND bill_day>=? AND bill_day<?`, t.InstanceID, t.Kind, t.SubjectID, t.From.In(billing.BusinessLocation).Format("2006-01-02"), end.In(billing.BusinessLocation).Format("2006-01-02"))
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var day time.Time
		var active bool
		if err = rows.Scan(&day, &active); err != nil {
			rows.Close()
			return p, err
		}
		status := "no_data"
		if active {
			status = "waiting"
		}
		key := day.Format("2006-01-02")
		byDay[key] = billing.GenerationDay{Day: key, Status: status}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	jobs, err := s.db.QueryContext(ctx, `SELECT j.id,j.range_from,j.status,j.error_message,j.updated_at,COALESCE((SELECT SUM(processed_rows) FROM billing_job_steps WHERE job_id=j.id),0) FROM billing_jobs j JOIN billing_statement_jobs st ON st.job_id=j.id WHERE j.instance_id=? AND st.statement_type=? AND st.subject_id=? AND j.usage_version>=3 AND j.status<>'superseded' AND NOT EXISTS (SELECT 1 FROM billing_generation_ranges retry WHERE retry.instance_id=j.instance_id AND retry.kind=j.job_type AND retry.subject_id=st.subject_id AND retry.cancelled=0 AND j.status='failed' AND j.updated_at<retry.generation_started_at AND j.range_from>=CONVERT_TZ(retry.range_from,'+08:00','+00:00') AND j.range_to<=CONVERT_TZ(retry.range_to,'+08:00','+00:00')) AND j.bill_period='daily' AND j.range_from>=? AND j.range_to<=? `+billingCurrentGenerationSQL+` ORDER BY FIELD(j.status,'publishing','running','pending','complete','no_data','failed'),j.created_at DESC`, t.InstanceID, t.Kind, t.SubjectID, t.From.UTC(), end.UTC())
	if err != nil {
		return p, err
	}
	seen := map[string]bool{}
	for jobs.Next() {
		var v billing.GenerationDay
		var from, updated time.Time
		if err = jobs.Scan(&v.JobID, &from, &v.Status, &v.Error, &updated, &v.Processed); err != nil {
			jobs.Close()
			return p, err
		}
		v.Day = from.In(billing.BusinessLocation).Format("2006-01-02")
		v.UpdatedAt = &updated
		if !seen[v.Day] {
			byDay[v.Day] = v
			seen[v.Day] = true
		}
	}
	err = jobs.Err()
	jobs.Close()
	if err != nil {
		return p, err
	}
	var cancelled bool
	var message sql.NullString
	var attempt sql.NullTime
	err = s.db.QueryRowContext(ctx, `SELECT last_error,last_attempt,cancelled FROM billing_generation_ranges WHERE instance_id=? AND kind=? AND subject_id=? AND range_from=? AND range_to=?`, t.InstanceID, t.Kind, t.SubjectID, t.From.In(billing.BusinessLocation).Format("2006-01-02"), t.To.In(billing.BusinessLocation).Format("2006-01-02")).Scan(&message, &attempt, &cancelled)
	if err != nil && err != sql.ErrNoRows {
		return p, err
	}
	p.Error = message.String
	if attempt.Valid {
		p.LastAttempt = &attempt.Time
	}
	for day := t.From.In(billing.BusinessLocation); day.Before(end); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		v, ok := byDay[key]
		if !ok {
			v = billing.GenerationDay{Day: key, Status: "unchecked"}
		} else {
			p.Checked++
		}
		p.TotalDays++
		p.Processed += v.Processed
		switch v.Status {
		case "complete":
			p.Complete++
		case "no_data":
			p.Empty++
		case "pending", "waiting":
			p.Pending++
		case "running", "publishing":
			p.Running++
		case "failed":
			p.Failed++
		}
		p.Days = append(p.Days, v)
	}
	if p.Running > 0 {
		p.Outcome = "generating"
	} else if p.Pending > 0 {
		p.Outcome = "queued"
	} else if p.Failed > 0 || p.Error != "" {
		p.Outcome = "failed"
	} else if p.Complete+p.Empty == p.TotalDays {
		if p.Complete > 0 {
			p.Outcome = "complete"
		} else {
			p.Outcome = "no_consumption"
		}
	}
	if p.Complete > 0 {
		m := billing.GenerationDay{Day: t.From.In(billing.BusinessLocation).Format("2006-01"), Status: "waiting"}
		var updated time.Time
		e := s.db.QueryRowContext(ctx, `SELECT j.id,j.status,j.error_message,j.updated_at FROM billing_jobs j JOIN billing_statement_jobs st ON st.job_id=j.id WHERE j.instance_id=? AND st.statement_type=? AND st.subject_id=? AND j.usage_version>=3 AND j.status<>'superseded' AND NOT EXISTS (SELECT 1 FROM billing_generation_ranges retry WHERE retry.instance_id=j.instance_id AND retry.kind=j.job_type AND retry.subject_id=st.subject_id AND retry.cancelled=0 AND j.status='failed' AND j.updated_at<retry.generation_started_at AND j.range_from>=CONVERT_TZ(retry.range_from,'+08:00','+00:00') AND j.range_to<=CONVERT_TZ(retry.range_to,'+08:00','+00:00')) AND j.bill_period='monthly' AND j.range_from=? AND j.range_to=? `+billingCurrentGenerationSQL+` ORDER BY FIELD(j.status,'complete','publishing','running','pending','failed'),j.created_at DESC LIMIT 1`, t.InstanceID, t.Kind, t.SubjectID, t.From.UTC(), t.To.UTC()).Scan(&m.JobID, &m.Status, &m.Error, &updated)
		if e != nil && e != sql.ErrNoRows {
			return p, e
		}
		if e == nil {
			m.UpdatedAt = &updated
		}
		p.Monthly = &m
		if p.Outcome == "complete" && m.Status != "complete" {
			p.Outcome = "awaiting_monthly"
			if m.Status == "failed" {
				p.Outcome = "failed"
			}
		}
	}
	if cancelled {
		p.Outcome = "cancelled"
	}
	return p, nil
}

// Includes registered work before its first job is queued, so refreshes and
// concurrent submissions cannot bypass the generation gate.
func (s Store) BillingGenerationBusy(ctx context.Context, site string) (bool, error) {
	state, err := s.BillingGenerationState(ctx, site)
	return state.Busy, err
}
func (s Store) BillingGenerationState(ctx context.Context, site string) (billing.GenerationState, error) {
	state := billing.GenerationState{Targets: []billing.AutomaticTarget{}, Jobs: []billing.Job{}}
	for _, status := range []string{"pending", "running", "publishing"} {
		jobs, err := s.ListBillingJobs(ctx, site, status, 200)
		if err != nil {
			return state, err
		}
		state.Jobs = append(state.Jobs, jobs...)
	}
	state.Busy = len(state.Jobs) > 0
	rows, err := s.db.QueryContext(ctx, `SELECT kind,subject_id,range_from,range_to,batch_id FROM billing_generation_ranges WHERE instance_id=? ORDER BY COALESCE(generation_started_at,created_at) DESC`, site)
	if err != nil {
		return state, err
	}
	targets := []billing.AutomaticTarget{}
	for rows.Next() {
		var t billing.AutomaticTarget
		var from, to time.Time
		t.InstanceID = site
		if err = rows.Scan(&t.Kind, &t.SubjectID, &from, &to, &t.BatchID); err != nil {
			rows.Close()
			return state, err
		}
		t.From, _ = time.ParseInLocation("2006-01-02", from.Format("2006-01-02"), billing.BusinessLocation)
		t.To, _ = time.ParseInLocation("2006-01-02", to.Format("2006-01-02"), billing.BusinessLocation)
		targets = append(targets, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return state, err
	}
	batchKey := func(t billing.AutomaticTarget) string {
		if t.BatchID != "" {
			return t.BatchID
		}
		return fmt.Sprintf("%s:%d:%s", t.Kind, t.SubjectID, t.From)
	}
	activeBatches := map[string]bool{}
	for _, t := range targets {
		p, e := s.BillingGenerationProgress(ctx, t)
		if e != nil {
			return state, e
		}
		switch p.Outcome {
		case "complete", "no_consumption", "failed", "cancelled":
		default:
			state.Busy = true
			activeBatches[batchKey(t)] = true
		}
	}
	for _, t := range targets {
		if activeBatches[batchKey(t)] {
			state.Targets = append(state.Targets, t)
		}
	}
	return state, nil
}

func (s Store) BeginBillingGeneration(ctx context.Context, site string) (func(), error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("billing-generate-%x", sha256.Sum256([]byte(site)))[:64]
	var locked int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", key).Scan(&locked); err != nil {
		conn.Close()
		return nil, err
	}
	if locked != 1 {
		conn.Close()
		return nil, billing.ErrGenerationInProgress
	}
	release := func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		defer conn.Close()
		conn.ExecContext(c, "DO RELEASE_LOCK(?)", key)
	}
	busy, err := s.BillingGenerationBusy(ctx, site)
	if err != nil {
		release()
		return nil, err
	}
	if busy {
		release()
		return nil, billing.ErrGenerationInProgress
	}
	return release, nil
}

func (s Store) BillingBatchMembers(ctx context.Context, targets []billing.AutomaticTarget) ([]billing.AutomaticTarget, error) {
	if len(targets) == 0 {
		return targets, nil
	}
	first := targets[0]
	var batch string
	err := s.db.QueryRowContext(ctx, `SELECT batch_id FROM billing_generation_ranges WHERE instance_id=? AND kind=? AND subject_id=? AND range_from=? AND range_to=?`, first.InstanceID, first.Kind, first.SubjectID, first.From.In(billing.BusinessLocation).Format("2006-01-02"), first.To.In(billing.BusinessLocation).Format("2006-01-02")).Scan(&batch)
	if err == sql.ErrNoRows || batch == "" {
		return targets, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT subject_id FROM billing_generation_ranges WHERE instance_id=? AND kind=? AND batch_id=? ORDER BY subject_id`, first.InstanceID, first.Kind, batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []billing.AutomaticTarget{}
	for rows.Next() {
		t := first
		t.BatchID = batch
		if err = rows.Scan(&t.SubjectID); err != nil {
			return nil, err
		}
		members = append(members, t)
	}
	return members, rows.Err()
}
