package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"encoding/json"
	"time"
)

func insertBillingTask(ctx context.Context, tx *sql.Tx, id string, targets []billing.AutomaticTarget) error {
	t := targets[0]
	ids := []int64{}
	for _, v := range targets {
		ids = append(ids, v.SubjectID)
	}
	raw, _ := json.Marshal(ids)
	end := billingActivityBoundary(t)
	_, err := tx.ExecContext(ctx, `INSERT INTO billing_generation_tasks(id,instance_id,kind,range_from,range_to,work_until,source,overwrite_existing,exclude_zero_output,subject_ids_json,created_at) VALUES(?,?,?,?,?,?,'manual',?,?,?,UTC_TIMESTAMP(6))`, id, t.InstanceID, t.Kind, t.From.UTC(), t.To.UTC(), end.UTC(), t.Overwrite, t.ExcludeZeroOutput, string(raw))
	return err
}

func (s Store) freezeBillingTasks(ctx context.Context, targets []billing.AutomaticTarget) error {
	seen := map[string]bool{}
	for _, t := range targets {
		var id string
		err := s.db.QueryRowContext(ctx, `SELECT batch_id FROM billing_generation_ranges WHERE instance_id=? AND kind=? AND subject_id=? AND range_from=? AND range_to=?`, t.InstanceID, t.Kind, t.SubjectID, t.From.In(billing.BusinessLocation).Format("2006-01-02"), t.To.In(billing.BusinessLocation).Format("2006-01-02")).Scan(&id)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		task, err := s.BillingGenerationTask(ctx, t.InstanceID, id)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		raw, err := json.Marshal(task.Items)
		if err != nil {
			return err
		}
		if _, err = s.db.ExecContext(ctx, `UPDATE billing_generation_tasks SET progress_json=? WHERE id=? AND progress_json IS NULL`, string(raw), id); err != nil {
			return err
		}
	}
	return nil
}

func (s Store) BillingGenerationTask(ctx context.Context, site, id string) (billing.GenerationTask, error) {
	var t billing.GenerationTask
	var ids, job string
	var frozen sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,instance_id,kind,range_from,range_to,work_until,source,overwrite_existing,exclude_zero_output,subject_ids_json,job_id,progress_json,created_at FROM billing_generation_tasks WHERE instance_id=? AND id=?`, site, id).Scan(&t.ID, &t.InstanceID, &t.Kind, &t.From, &t.To, &t.WorkUntil, &t.Source, &t.Overwrite, &t.ExcludeZeroOutput, &ids, &job, &frozen, &t.CreatedAt)
	if err != nil {
		return t, err
	}
	if err = json.Unmarshal([]byte(ids), &t.SubjectIDs); err != nil {
		return t, err
	}
	t.Items = []billing.GenerationProgress{}
	if frozen.Valid {
		if err = json.Unmarshal([]byte(frozen.String), &t.Items); err != nil {
			return t, err
		}
	} else if job != "" {
		j, e := s.BillingJob(ctx, job)
		if e != nil {
			return t, e
		}
		outcome := j.Status
		if outcome == "superseded" {
			outcome = "complete"
		}
		if outcome == "no_data" {
			outcome = "no_consumption"
		}
		if outcome == "pending" {
			outcome = "queued"
		}
		if outcome == "running" || outcome == "publishing" {
			outcome = "generating"
		}
		if j.ErrorMessage == "cancelled manually" {
			outcome = "cancelled"
		}
		var processed int64
		if e = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(processed_rows),0) FROM billing_job_steps WHERE job_id=?`, job).Scan(&processed); e != nil {
			return t, e
		}
		p := billing.GenerationProgress{SubjectID: billingJobSubject(j), Outcome: outcome, TotalDays: 1, Checked: 1, Processed: processed, Days: []billing.GenerationDay{{Day: j.From.In(billing.BusinessLocation).Format("2006-01-02"), JobID: j.ID, Status: outcome, Processed: processed, Error: j.ErrorMessage, UpdatedAt: &j.UpdatedAt}}}
		switch outcome {
		case "complete":
			p.Complete = 1
		case "no_consumption":
			p.Empty = 1
		case "failed":
			p.Failed = 1
		case "queued":
			p.Pending = 1
		case "generating":
			p.Running = 1
		}
		t.Items = append(t.Items, p)
	} else {
		for _, uid := range t.SubjectIDs {
			p, e := s.BillingGenerationProgress(ctx, billing.AutomaticTarget{InstanceID: site, Kind: t.Kind, SubjectID: uid, From: t.From, To: t.To, ProgressUntil: t.WorkUntil})
			if e != nil {
				return t, e
			}
			t.Items = append(t.Items, p)
		}
	}
	summarizeBillingTask(&t)
	if !frozen.Valid && (t.Outcome == "complete" || t.Outcome == "no_consumption" || t.Outcome == "cancelled" || (job != "" && t.Outcome == "failed")) {
		raw, e := json.Marshal(t.Items)
		if e != nil {
			return t, e
		}
		if _, e = s.db.ExecContext(ctx, `UPDATE billing_generation_tasks SET progress_json=? WHERE id=? AND progress_json IS NULL`, string(raw), t.ID); e != nil {
			return t, e
		}
	}
	return t, nil
}

func summarizeBillingTask(t *billing.GenerationTask) {
	t.Outcome = "complete"
	done, total, cancelled, active := 0, 0, 0, false
	for _, p := range t.Items {
		t.CompleteDays += p.Complete
		t.EmptyDays += p.Empty
		total += p.TotalDays
		done += p.Complete + p.Empty
		if p.Monthly != nil {
			total++
			if p.Monthly.Status == "complete" {
				done++
			}
		}
		switch p.Outcome {
		case "complete", "no_consumption":
			t.CompletedUsers++
		case "failed":
			t.FailedUsers++
		case "cancelled":
			cancelled++
		default:
			active = true
		}
	}
	if total > 0 {
		t.Percentage = done * 100 / total
	}
	if active {
		t.Outcome = "generating"
	} else if cancelled > 0 {
		t.Outcome = "cancelled"
	} else if t.FailedUsers > 0 {
		t.Outcome = "failed"
	} else {
		t.Percentage = 100
		if t.CompleteDays == 0 {
			t.Outcome = "no_consumption"
		}
	}
}

func (s Store) ListBillingGenerationTasks(ctx context.Context, site, kind string, limit, offset int) ([]billing.GenerationTask, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_generation_tasks WHERE instance_id=? AND kind=?`, site, kind).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM billing_generation_tasks WHERE instance_id=? AND kind=? ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, site, kind, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	out := []billing.GenerationTask{}
	for _, id := range ids {
		t, e := s.BillingGenerationTask(ctx, site, id)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, t)
	}
	return out, total, nil
}

// Standalone automatic/temporary invocations also get one task record. Daily
// child jobs covered by a manual batch stay within that batch's history entry.
func recordStandaloneBillingTask(ctx context.Context, tx *sql.Tx, j billing.Job) error {
	if (j.JobType != "user_statement" && j.JobType != "upstream_statement") || j.UsageVersion < 3 {
		return nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_generation_tasks WHERE instance_id=? AND kind=? AND job_id='' AND JSON_CONTAINS(subject_ids_json,CAST(? AS JSON)) AND range_from<=? AND work_until>=? AND created_at<=?`, j.InstanceID, j.JobType, billingJobSubject(j), j.From.UTC(), j.To.UTC(), j.CreatedAt.UTC()).Scan(&count); err != nil {
		return err
	}
	if count > 0 && j.BillPeriod != "temporary" {
		return nil
	}
	source := "automatic"
	if j.BillPeriod == "temporary" {
		source = "temporary"
	}
	raw, _ := json.Marshal([]int64{billingJobSubject(j)})
	_, err := tx.ExecContext(ctx, `INSERT IGNORE INTO billing_generation_tasks(id,instance_id,kind,range_from,range_to,work_until,source,exclude_zero_output,subject_ids_json,job_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, "job:"+j.ID, j.InstanceID, j.JobType, j.From.UTC(), j.To.UTC(), j.To.UTC(), source, j.ExcludeZeroOutput, string(raw), j.ID, time.Now().UTC())
	return err
}

func (s Store) freezeStandaloneTask(ctx context.Context, id string) error {
	var site string
	err := s.db.QueryRowContext(ctx, `SELECT instance_id FROM billing_generation_tasks WHERE id=?`, "job:"+id).Scan(&site)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = s.BillingGenerationTask(ctx, site, "job:"+id)
	return err
}
