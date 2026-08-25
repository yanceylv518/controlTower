package mysqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"controltower/server/internal/billing"
)

func (s Store) CreateBillingStatementJob(ctx context.Context, job billing.Job, steps []billing.JobStep, subjectName string) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked int
	if err = conn.QueryRowContext(ctx, `SELECT GET_LOCK('ct:billing-statement-queue',10)`).Scan(&locked); err != nil || locked != 1 {
		return fmt.Errorf("lock billing statement queue: %w", err)
	}
	defer conn.ExecContext(context.Background(), `SELECT RELEASE_LOCK('ct:billing-statement-queue')`)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var duplicateID, duplicateStatus string
	err = tx.QueryRowContext(ctx, `SELECT id,status FROM billing_jobs WHERE request_key=? LIMIT 1 FOR UPDATE`, job.RequestKey).Scan(&duplicateID, &duplicateStatus)
	if err == nil {
		if duplicateStatus != "failed" {
			return billing.ErrStatementDuplicate
		}
		// A failed attempt did not produce a bill. Remove it atomically so the
		// same request key can be retried without a manual cleanup step.
		if _, err = tx.ExecContext(ctx, `DELETE FROM billing_jobs WHERE id=? AND status='failed'`, duplicateID); err != nil {
			return err
		}
	} else if err != sql.ErrNoRows {
		return err
	}
	var queued int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_jobs WHERE job_type IN ('user_statement','upstream_statement') AND status='pending'`).Scan(&queued); err != nil {
		return err
	}
	if queued >= 5 {
		return billing.ErrStatementQueueFull
	}
	if err = createBillingJobTx(ctx, tx, job, steps); err != nil {
		return err
	}
	subjectID := job.UserID
	if job.JobType == "upstream_statement" {
		subjectID = job.UpstreamID
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO billing_statement_jobs(job_id,statement_type,subject_id,subject_name,created_at) VALUES(?,?,?,?,?)`, job.ID, job.JobType, subjectID, subjectName, job.CreatedAt)
	if err != nil {
		return err
	}
	if job.JobType == "upstream_statement" {
		result, copyErr := tx.ExecContext(ctx, `INSERT INTO billing_statement_channels(job_id,channel_id,channel_name) SELECT ?,channel_id,channel_name FROM billing_upstream_channel_bindings WHERE instance_id=? AND upstream_id=?`, job.ID, job.InstanceID, job.UpstreamID)
		if copyErr != nil {
			return copyErr
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return fmt.Errorf("upstream has no channels")
		}
	}
	if job.JobType == "upstream_statement" {
		_, err = tx.ExecContext(ctx, `INSERT INTO billing_statement_discount_snapshots(job_id,discount_type,subject_id,channel_id,channel_name,model_name,discount,effective_from,effective_to,source_rule_id,created_at) SELECT ?,'upstream_channel',r.subject_id,r.channel_id,b.channel_name,'',r.discount,r.effective_from,r.effective_to,r.id,? FROM billing_discount_rules r JOIN billing_statement_channels b ON b.job_id=? AND b.channel_id=r.channel_id WHERE r.instance_id=? AND r.discount_type='upstream_channel' AND r.subject_id=? AND r.effective_from<? AND COALESCE(r.effective_to,DATE('9999-12-31'))>?`, job.ID, job.CreatedAt, job.ID, job.InstanceID, job.UpstreamID, job.To.In(billing.BusinessLocation).Format("2006-01-02"), job.From.In(billing.BusinessLocation).Format("2006-01-02"))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s Store) BillingStatementUpstream(ctx context.Context, site string, id int64) (billing.Upstream, error) {
	var v billing.Upstream
	err := s.db.QueryRowContext(ctx, `SELECT id,instance_id,name,enabled,remark,created_at,updated_at,updated_by FROM billing_upstreams WHERE instance_id=? AND id=? AND enabled=1`, site, id).Scan(&v.ID, &v.InstanceID, &v.Name, &v.Enabled, &v.Remark, &v.CreatedAt, &v.UpdatedAt, &v.UpdatedBy)
	if err != nil {
		return v, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT channel_id,channel_name FROM billing_upstream_channel_bindings WHERE instance_id=? AND upstream_id=? ORDER BY channel_id`, site, id)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var c billing.UpstreamChannel
		if err = rows.Scan(&c.ChannelID, &c.ChannelName); err != nil {
			return v, err
		}
		v.Channels = append(v.Channels, c)
	}
	return v, rows.Err()
}

func (s Store) BillingStatementChannelIDs(ctx context.Context, jobID string) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT channel_id FROM billing_statement_channels WHERE job_id=?`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (s Store) ListBillingStatementUserFiles(ctx context.Context, jobID string) ([]billing.UserDailyFile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT job_id,instance_id,bill_day,user_id,relative_path,file_size,sha256,created_at FROM billing_user_daily_files WHERE job_id=? ORDER BY bill_day,user_id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.UserDailyFile{}
	for rows.Next() {
		var item billing.UserDailyFile
		if err = rows.Scan(&item.JobID, &item.InstanceID, &item.BillDay, &item.UserID, &item.RelativePath, &item.FileSize, &item.SHA256, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) QueryBillingStatementReconciliation(ctx context.Context, jobID string, limit, offset int) ([]billing.ReconciliationOrder, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `SELECT job_id,source_log_id,instance_id,UNIX_TIMESTAMP(created_at),request_id,upstream_request_id,user_id,username,token_id,token_name,channel_id,channel_name,model_name,prompt_tokens,completion_tokens,cache_read_tokens,cache_write_tokens,calculated_quota,logged_quota,quota_difference,CAST(calculated_amount AS CHAR),CAST(logged_amount AS CHAR),CAST(amount_difference AS CHAR),reason,detected_at FROM billing_statement_reconciliation_orders WHERE job_id=? ORDER BY created_at,source_log_id LIMIT ? OFFSET ?`, jobID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.ReconciliationOrder{}
	for rows.Next() {
		var v billing.ReconciliationOrder
		if err = rows.Scan(&v.JobID, &v.SourceLogID, &v.InstanceID, &v.CreatedUnix, &v.RequestID, &v.UpstreamRequestID, &v.UserID, &v.Username, &v.TokenID, &v.TokenName, &v.ChannelID, &v.ChannelName, &v.ModelName, &v.PromptTokens, &v.CompletionTokens, &v.CacheReadTokens, &v.CacheWriteTokens, &v.CalculatedQuota, &v.LoggedQuota, &v.QuotaDifference, &v.CalculatedAmount, &v.LoggedAmount, &v.AmountDifference, &v.Reason, &v.DetectedAt); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func (s Store) CountBillingStatementReconciliation(ctx context.Context, jobID string) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_statement_reconciliation_orders WHERE job_id=?`, jobID).Scan(&count)
	return count, err
}

func (s Store) DeleteBillingStatement(ctx context.Context, jobID string) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var kind, status string
	if err = tx.QueryRowContext(ctx, `SELECT j.job_type,j.status FROM billing_jobs j JOIN billing_statement_jobs s ON s.job_id=j.id WHERE j.id=? FOR UPDATE`, jobID).Scan(&kind, &status); err != nil {
		return nil, err
	}
	if (kind != "user_statement" && kind != "upstream_statement") || status != "complete" {
		return nil, sql.ErrNoRows
	}
	paths := []string{}
	rows, err := tx.QueryContext(ctx, `SELECT relative_path FROM billing_user_daily_files WHERE job_id=? UNION SELECT relative_path FROM billing_channel_daily_files WHERE job_id=?`, jobID, jobID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var path string
		if err = rows.Scan(&path); err != nil {
			_ = rows.Close()
			return nil, err
		}
		paths = append(paths, path)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM billing_jobs WHERE id=? AND status='complete' AND job_type IN ('user_statement','upstream_statement')`, jobID)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, sql.ErrNoRows
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return paths, nil
}

func (s Store) enrichBillingStatement(ctx context.Context, job *billing.Job) error {
	if job.JobType != "user_statement" && job.JobType != "upstream_statement" {
		return nil
	}
	var kind string
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT statement_type,subject_id,subject_name FROM billing_statement_jobs WHERE job_id=?`, job.ID).Scan(&kind, &id, &job.UpstreamName)
	if err != nil {
		return err
	}
	if kind == "upstream_statement" {
		job.UpstreamID = id
	} else {
		job.UserID = id
		if job.UpstreamName == "" {
			// Older statement rows did not snapshot the selected user's name.
			// Recover it from that statement's own immutable billing aggregates.
			_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(username),'') FROM billing_compact_daily_totals WHERE job_id=? AND user_id=?`, job.ID, id).Scan(&job.UpstreamName)
		}
		job.UserName = job.UpstreamName
		job.UpstreamName = ""
	}
	name := job.UserName
	if kind == "upstream_statement" {
		name = job.UpstreamName
	}
	if name == "" {
		name = fmt.Sprintf("对象%d", id)
	}
	job.BillNo = fmt.Sprintf("%s-%s至%s-%s", name, job.From.In(billing.BusinessLocation).Format("20060102"), job.To.Add(-time.Nanosecond).In(billing.BusinessLocation).Format("20060102"), job.CreatedAt.In(billing.BusinessLocation).Format("20060102150405"))
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_statement_reconciliation_orders WHERE job_id=?`, job.ID).Scan(&job.MismatchRows)
	return nil
}

func (s Store) finalizeBillingStatement(ctx context.Context, tx *sql.Tx, job billing.Job, now time.Time) error {
	var kind, name string
	var subjectID int64
	if err := tx.QueryRowContext(ctx, `SELECT statement_type,subject_id,subject_name FROM billing_statement_jobs WHERE job_id=?`, job.ID).Scan(&kind, &subjectID, &name); err != nil {
		return err
	}
	var normal int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(request_count),0) FROM billing_compact_daily_totals WHERE job_id=?`, job.ID).Scan(&normal); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO billing_statements(id,job_id,instance_id,statement_type,subject_id,subject_name,range_from,range_to,normal_orders,abnormal_orders,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, job.ID, job.ID, job.InstanceID, kind, subjectID, name, job.From, job.To, normal, job.AbnormalRows, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE billing_jobs SET status='complete',finished_at=?,updated_at=? WHERE id=? AND completed_steps>=total_steps`, now, now, job.ID)
	return err
}
