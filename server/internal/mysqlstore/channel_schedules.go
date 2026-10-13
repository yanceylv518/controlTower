package mysqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"controltower/server/internal/channelschedule"
	"controltower/server/internal/channelupdates"
	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
	"github.com/go-sql-driver/mysql"
)

func (s Store) LockChannelControl(ctx context.Context, site string, channel int64) (func(), error) {
	release, err := s.lockChannelWrite(ctx, site, channel, 0)
	if errors.Is(err, tuning.ErrChannelControlBusy) {
		return nil, channelschedule.ErrBusy
	}
	return release, err
}
func (s Store) LockChannelScheduleSite(ctx context.Context, site string, wait time.Duration) (func(), error) {
	return s.channelControlSiteLock(ctx, site, wait, false)
}

func (s Store) LockChannelControlSite(ctx context.Context, site string, wait time.Duration) (func(), error) {
	return s.channelControlSiteLock(ctx, site, wait, true)
}

// 普通写入共享站点闸门，计划独占；独立锁表避免持锁期间的业务落盘阻塞自身。
func (s Store) channelControlSiteLock(ctx context.Context, site string, wait time.Duration, shared bool) (func(), error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM channel_control_site_guards WHERE site_id=?)", site).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		if _, err := s.db.ExecContext(ctx, "INSERT IGNORE INTO channel_control_site_guards(site_id) VALUES(?)", site); err != nil {
			return nil, err
		}
	}
	// 获取时遵守请求超时，持锁生命周期由释放函数负责，直到写入结果完成落盘。
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := conn.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	query := "SELECT site_id FROM channel_control_site_guards WHERE site_id=? FOR UPDATE"
	if shared {
		query = "SELECT site_id FROM channel_control_site_guards WHERE site_id=? FOR SHARE"
	}
	acquireCtx := ctx
	if wait <= 0 {
		query += " NOWAIT"
	} else {
		var cancel context.CancelFunc
		acquireCtx, cancel = context.WithTimeout(ctx, wait)
		defer cancel()
	}
	var held string
	if err = tx.QueryRowContext(acquireCtx, query, site).Scan(&held); err != nil {
		_ = tx.Rollback()
		_ = conn.Close()
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && (mysqlErr.Number == 3572 || mysqlErr.Number == 1205) {
			return nil, channelschedule.ErrBusy
		}
		return nil, err
	}
	return func() {
		_ = tx.Rollback()
		_ = conn.Close()
	}, nil
}

func (s Store) HasDirectChannelControl(site string) (bool, error) {
	var available bool
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM instances WHERE COALESCE(NULLIF(site_id,''),id)=? AND enabled=1 AND deleted=0 AND control_api_url<>'' AND control_api_token<>'')", site).Scan(&available)
	return available, err
}

const scheduleColumns = `site_id,channel_id,enabled,config_json,revision,effective_at,updated_at,updated_by,closed_by_schedule`

type scheduleScanner interface{ Scan(...any) error }

func scanChannelSchedule(row scheduleScanner) (channelschedule.Schedule, error) {
	var result channelschedule.Schedule
	var raw string
	var enabled bool
	var revision int64
	err := row.Scan(&result.SiteID, &result.ChannelID, &enabled, &raw, &revision, &result.EffectiveAt, &result.UpdatedAt, &result.UpdatedBy, &result.ClosedBySchedule)
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal([]byte(raw), &result.Config); err != nil {
		return result, err
	}
	result.Enabled, result.Revision = enabled, revision
	if err = result.Config.Normalize(); err != nil {
		return result, err
	}
	return result, nil
}

func (s Store) LoadChannelSchedule(ctx context.Context, site string, channel int64) (channelschedule.Schedule, error) {
	return scanChannelSchedule(s.db.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM channel_schedules WHERE site_id=? AND channel_id=?", site, channel))
}

func (s Store) ListChannelSchedules(ctx context.Context, site string) ([]channelschedule.Schedule, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+scheduleColumns+" FROM channel_schedules WHERE site_id=? ORDER BY channel_id LIMIT ?", site, channelschedule.MaxPerSite)
	if err != nil {
		return nil, err
	}
	items := []channelschedule.Schedule{}
	for rows.Next() {
		value, err := scanChannelSchedule(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	columns := "e." + strings.ReplaceAll(executionColumns, ",", ",e.")
	executions, err := s.db.QueryContext(ctx, "SELECT "+columns+" FROM channel_schedule_executions e JOIN channel_schedules s ON s.last_execution_id=e.id WHERE s.site_id=? LIMIT ?", site, channelschedule.MaxPerSite)
	if err != nil {
		return nil, err
	}
	defer executions.Close()
	byChannel := map[int64]channelschedule.Execution{}
	for executions.Next() {
		run, err := scanScheduleExecution(executions)
		if err != nil {
			return nil, err
		}
		byChannel[run.ChannelID] = run
	}
	if err := executions.Err(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for i := range items {
		items[i].Describe(now)
		if run, ok := byChannel[items[i].ChannelID]; ok {
			items[i].Execution = &run
		}
	}
	return items, nil
}

func (s Store) DueChannelSchedules(ctx context.Context, now time.Time) ([]channelschedule.Schedule, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+scheduleColumns+" FROM channel_schedules s WHERE enabled=1 AND next_check_at<=? AND EXISTS(SELECT 1 FROM instances i WHERE COALESCE(NULLIF(i.site_id,''),i.id)=s.site_id AND i.enabled=1 AND i.deleted=0) ORDER BY next_check_at,site_id,channel_id LIMIT 64", now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []channelschedule.Schedule
	for rows.Next() {
		value, err := scanChannelSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s Store) ListChannelScheduleControls(site string, now time.Time) (map[int64]channelschedule.Control, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	plans, err := s.ListChannelSchedules(ctx, site)
	if err != nil {
		return nil, err
	}
	out := map[int64]channelschedule.Control{}
	for _, v := range plans {
		out[v.ChannelID] = channelschedule.Control{Enabled: v.Enabled, Allowed: v.Position(now).Allowed, Closed: v.ClosedBySchedule, Opening: v.PendingOpen(now)}
	}
	return out, nil
}

func scheduleAudit(id, site string, channel int64, operation, actor, status, before, after, message string, now time.Time) storage.OperationAudit {
	a := storage.OperationAudit{ID: id, InstanceID: site, OperationType: operation, TargetType: "channel_schedule", TargetID: strconv.FormatInt(channel, 10), ActorID: actor, Status: status, BeforeSummary: before, AfterSummary: after, ErrorSummary: message, SourceComponent: "tuning", CreatedAt: now, UpdatedAt: now}
	if actor == "system:schedule" {
		a.ActorType = "system"
		a.TriggerType = "automatic"
	}
	return a
}

func (s Store) SaveChannelSchedule(ctx context.Context, site string, channel int64, config channelschedule.Config, audit storage.OperationAudit, now time.Time) (int64, error) {
	if err := config.Normalize(); err != nil {
		return 0, err
	}
	siteRelease, err := s.LockChannelScheduleSite(ctx, site, 0)
	if err != nil {
		return 0, err
	}
	defer siteRelease()
	release, err := s.LockChannelControl(ctx, site, channel)
	if err != nil {
		return 0, err
	}
	defer release()
	if config.Enabled {
		available, err := s.HasDirectChannelControl(site)
		if err != nil {
			return 0, err
		}
		if !available {
			return 0, channelschedule.ErrUnsupported
		}
	}
	channels, err := s.LatestChannels(site)
	if err != nil {
		return 0, err
	}
	found := false
	for _, c := range channels {
		if c.ID == channel {
			found = true
			break
		}
	}
	if !found {
		return 0, channelschedule.ErrChannelNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	old, err := scanChannelSchedule(tx.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM channel_schedules WHERE site_id=? AND channel_id=? FOR UPDATE", site, channel))
	if errors.Is(err, sql.ErrNoRows) {
		if config.Revision != 0 || config.ExpectedUpdatedAt != nil {
			return 0, channelschedule.ErrConflict
		}
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM channel_schedules WHERE site_id=?", site).Scan(&count); err != nil {
			return 0, err
		}
		if count >= channelschedule.MaxPerSite {
			return 0, channelschedule.ErrLimit
		}
	} else if err != nil {
		return 0, err
	} else if old.Revision != config.Revision || config.ExpectedUpdatedAt != nil && !old.UpdatedAt.Equal(*config.ExpectedUpdatedAt) {
		return 0, channelschedule.ErrConflict
	}
	if old.Revision > 0 && !config.AlignNow && config.SameTiming(old.Config) {
		return old.Revision, tx.Commit()
	}
	config.Revision++
	config.ExpectedUpdatedAt = nil
	raw, err := json.Marshal(config)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO channel_schedules(site_id,channel_id,enabled,config_json,revision,effective_at,updated_at,updated_by,next_check_at) VALUES(?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE enabled=VALUES(enabled),config_json=VALUES(config_json),revision=VALUES(revision),effective_at=VALUES(effective_at),updated_at=VALUES(updated_at),updated_by=VALUES(updated_by),last_execution_id='',next_check_at=VALUES(next_check_at)`, site, channel, config.Enabled, string(raw), config.Revision, now, now, audit.ActorID, now)
	if err != nil {
		return 0, err
	}
	if err = supersedeScheduleExecutions(ctx, tx, site, channel, "", "计划已修改", now); err != nil {
		return 0, err
	}
	if err = resolveScheduleAlert(ctx, tx, site, channel, now); err != nil {
		return 0, err
	}
	before, _ := json.Marshal(old.Config)
	audit.OperationType = "channel.schedule_save"
	audit.TargetType = "channel_schedule"
	audit.TargetID = strconv.FormatInt(channel, 10)
	audit.InstanceID = site
	audit.BeforeSummary = string(before)
	audit.AfterSummary = string(raw)
	audit.Status = "succeeded"
	if err = insertOperationAuditTx(tx, audit); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err == nil {
		channelupdates.Notify(site)
	}
	return config.Revision, err
}

func (s Store) DeleteChannelSchedule(ctx context.Context, site string, channel, revision int64, expectedUpdatedAt *time.Time, audit storage.OperationAudit, now time.Time) error {
	siteRelease, err := s.LockChannelScheduleSite(ctx, site, 0)
	if err != nil {
		return err
	}
	defer siteRelease()
	release, err := s.LockChannelControl(ctx, site, channel)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	old, err := scanChannelSchedule(tx.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM channel_schedules WHERE site_id=? AND channel_id=? FOR UPDATE", site, channel))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return channelschedule.ErrConflict
		}
		return err
	}
	if old.Revision != revision || expectedUpdatedAt != nil && !old.UpdatedAt.Equal(*expectedUpdatedAt) {
		return channelschedule.ErrConflict
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM channel_schedules WHERE site_id=? AND channel_id=?", site, channel); err != nil {
		return err
	}
	if err = supersedeScheduleExecutions(ctx, tx, site, channel, "", "计划已删除", now); err != nil {
		return err
	}
	if err = resolveScheduleAlert(ctx, tx, site, channel, now); err != nil {
		return err
	}
	raw, _ := json.Marshal(old)
	audit.OperationType = "channel.schedule_delete"
	audit.TargetType = "channel_schedule"
	audit.TargetID = strconv.FormatInt(channel, 10)
	audit.InstanceID = site
	audit.BeforeSummary = string(raw)
	audit.Status = "succeeded"
	if err = insertOperationAuditTx(tx, audit); err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		channelupdates.Notify(site)
	}
	return err
}

const executionColumns = `id,site_id,channel_id,revision,boundary_at,target_status,status,attempts,error_summary,before_status,expires_at,updated_at`

func scanScheduleExecution(row scheduleScanner) (channelschedule.Execution, error) {
	var v channelschedule.Execution
	var expires sql.NullTime
	err := row.Scan(&v.ID, &v.SiteID, &v.ChannelID, &v.Revision, &v.Boundary, &v.Target, &v.Status, &v.Attempts, &v.Error, &v.BeforeStatus, &expires, &v.UpdatedAt)
	if expires.Valid {
		v.ExpiresAt = &expires.Time
	}
	return v, err
}

func (s Store) ChannelScheduleExecutions(ctx context.Context, site string, channel int64) ([]channelschedule.Execution, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+executionColumns+" FROM channel_schedule_executions WHERE site_id=? AND channel_id=? ORDER BY updated_at DESC,id DESC LIMIT 50", site, channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []channelschedule.Execution{}
	for rows.Next() {
		v, err := scanScheduleExecution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s Store) PrepareChannelSchedule(ctx context.Context, plan channelschedule.Schedule, now time.Time) (*channelschedule.Execution, error) {
	p := plan.Position(now)
	nextCheck := now.Truncate(30 * time.Second).Add(30 * time.Second)
	if !p.NextAt.IsZero() && p.NextAt.Before(nextCheck) {
		nextCheck = p.NextAt
	}
	boundary := p.Boundary
	if boundary.IsZero() || boundary.Before(plan.EffectiveAt) {
		if !plan.AlignNow {
			next := p.NextAt
			if next.IsZero() {
				next = now.Add(time.Hour)
			}
			_, err := s.db.ExecContext(ctx, "UPDATE channel_schedules SET next_check_at=? WHERE site_id=? AND channel_id=? AND revision=?", next, plan.SiteID, plan.ChannelID, plan.Revision)
			return nil, err
		}
		boundary = plan.EffectiveAt
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%s", plan.SiteID, plan.ChannelID, plan.Revision, boundary.UTC().Format(time.RFC3339Nano))))
	id := "schedule-" + hex.EncodeToString(hash[:20])
	target := 2
	if p.Allowed {
		target = 1
	}
	var expires any
	if !p.NextAt.IsZero() {
		expires = p.NextAt
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = supersedeScheduleExecutions(ctx, tx, plan.SiteID, plan.ChannelID, id, "已到下一计划边界", now); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT IGNORE INTO channel_schedule_executions(id,site_id,channel_id,revision,boundary_at,target_status,status,expires_at,updated_at) VALUES(?,?,?,?,?,?,'pending',?,?)", id, plan.SiteID, plan.ChannelID, plan.Revision, boundary, target, expires, now); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE channel_schedules SET last_execution_id=?,next_check_at=? WHERE site_id=? AND channel_id=? AND revision=?", id, nextCheck, plan.SiteID, plan.ChannelID, plan.Revision); err != nil {
		return nil, err
	}
	run, err := scanScheduleExecution(tx.QueryRowContext(ctx, "SELECT "+executionColumns+" FROM channel_schedule_executions WHERE id=?", id))
	if err != nil {
		return nil, err
	}
	if channelschedule.Terminal(run.Status) {
		next := p.NextAt
		if next.IsZero() {
			next = now.Add(time.Hour)
		}
		if _, err = tx.ExecContext(ctx, "UPDATE channel_schedules SET next_check_at=? WHERE site_id=? AND channel_id=? AND revision=?", next, plan.SiteID, plan.ChannelID, plan.Revision); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &run, nil
}

func (s Store) BeginChannelScheduleWrite(ctx context.Context, run *channelschedule.Execution, before int, now time.Time) error {
	run.BeforeStatus = before
	run.Attempts++
	run.Status = "running"
	run.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, "UPDATE channel_schedule_executions SET status='running',attempts=?,before_status=?,updated_at=? WHERE id=?", run.Attempts, before, now, run.ID)
	return err
}

func (s Store) FinishChannelSchedule(ctx context.Context, run channelschedule.Execution, status, message string, written bool, now time.Time) error {
	message = strings.TrimSpace(message)
	if len(message) > 500 {
		message = string([]rune(message)[:min(len([]rune(message)), 160)])
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revision int64
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM channel_schedules WHERE site_id=? AND channel_id=? FOR UPDATE", run.SiteID, run.ChannelID).Scan(&revision); err != nil {
		return err
	}
	if revision != run.Revision {
		return channelschedule.ErrSuperseded
	}
	if written {
		// 回读已确认的写入同步解除生命线的待确认标记。
		if _, err = tx.ExecContext(ctx, "UPDATE channel_control_requests SET outcome='succeeded',reason='status_confirmed' WHERE command_id=? AND outcome IN ('pending','unconfirmed')", run.ID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE channel_current c JOIN instances i ON i.id=c.instance_id SET c.status=?,c.captured_at=GREATEST(c.captured_at,?) WHERE CASE WHEN i.site_id='' THEN i.id ELSE i.site_id END=? AND c.channel_id=?`, channelStatusLabel(run.Target), now, run.SiteID, run.ChannelID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE channel_schedules SET closed_by_schedule=? WHERE site_id=? AND channel_id=?", run.Target == 2, run.SiteID, run.ChannelID); err != nil {
			return err
		}
		// 已确认的计划动作使旧评估失效；保留熔断归属及容量滞回，不清除在途回执。
		if _, err = tx.ExecContext(ctx, `UPDATE tuning_continuous_states SET control_revision=control_revision+1,paused_reason=IF(?=1 AND paused_reason='manual_disabled','',paused_reason),last_bucket_at=?,last_observed_requests=0,updated_at=? WHERE instance_id=? AND channel_id=?`, run.Target, now, now, run.SiteID, run.ChannelID); err != nil {
			return err
		}
		if run.Target == 1 {
			// 普通计划开启丢弃过期平滑指标；熔断与容量恢复证据保留原生命周期。
			if _, err = tx.ExecContext(ctx, `UPDATE tuning_continuous_states SET k_error=1,k_speed=1,k_cache=1,k_otps=1,multiplier=1,smoothed_error_rate=0,last_observed_errors=0,metric_ready=0,baseline_ready=0,cache_ready=0,otps_ready=0,probe_speed_json='{}',evaluation_json=NULL WHERE instance_id=? AND channel_id=? AND phase IN ('','normal') AND circuit_disabled=0 AND circuit_status_target=0 AND circuit_status_command_id=''`, run.SiteID, run.ChannelID); err != nil {
				return err
			}
		}
	} else if run.Target == 1 && (status == "unchanged" || status == "succeeded") {
		if _, err = tx.ExecContext(ctx, "UPDATE channel_schedules SET closed_by_schedule=0 WHERE site_id=? AND channel_id=?", run.SiteID, run.ChannelID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE channel_schedule_executions SET status=?,attempts=?,before_status=?,error_summary=?,updated_at=? WHERE id=?", status, run.Attempts, run.BeforeStatus, message, now, run.ID); err != nil {
		return err
	}
	operation := "channel.schedule_close"
	if run.Target == 1 {
		operation = "channel.schedule_open"
	}
	before, _ := json.Marshal(map[string]int{"status": run.BeforeStatus})
	after, _ := json.Marshal(map[string]any{"status": run.Target, "attempts": run.Attempts, "boundary": run.Boundary, "revision": run.Revision, "result": status})
	audit := scheduleAudit(run.ID, run.SiteID, run.ChannelID, operation, "system:schedule", status, string(before), string(after), message, now)
	if err = insertScheduleExecutionAuditTx(tx, audit); err != nil {
		return err
	}
	if status == "skipped" || status == "failed" || status == "unknown" {
		alertHash := sha256.Sum256([]byte("channel-schedule:" + run.SiteID + ":" + strconv.FormatInt(run.ChannelID, 10)))
		alertID := hex.EncodeToString(alertHash[:20])
		instance, err := controlInstanceForSite(tx, run.SiteID)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO alerts(id,instance_id,rule_key,severity,status,title,summary,first_seen_at,last_seen_at) VALUES(?,?,'channel_schedule','warning','firing','渠道定时启停未完成',?,?,?) ON DUPLICATE KEY UPDATE summary=VALUES(summary),last_seen_at=VALUES(last_seen_at),status=IF(status='resolved','firing',status),resolved_at=NULL`, alertID, instance, fmt.Sprintf("渠道 #%d：%s", run.ChannelID, message), now, now); err != nil {
			return err
		}
	} else if status == "succeeded" || status == "unchanged" {
		if err = resolveScheduleAlert(ctx, tx, run.SiteID, run.ChannelID, now); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err == nil {
		channelupdates.Notify(run.SiteID)
	}
	return err
}

func checkScheduleRecovery(q priorityQuery, vSite string, channel int64, now time.Time) (bool, error) {
	var enabled, closed bool
	var raw string
	err := q.QueryRowContext(context.Background(), "SELECT enabled,closed_by_schedule,config_json FROM channel_schedules WHERE site_id=? AND channel_id=?", vSite, channel).Scan(&enabled, &closed, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var cfg channelschedule.Config
	if err = json.Unmarshal([]byte(raw), &cfg); err != nil {
		return false, err
	}
	if err = cfg.Normalize(); err != nil {
		return false, err
	}
	if enabled && !cfg.Position(now).Allowed {
		return closed, channelschedule.ErrSuperseded
	}
	return closed, nil
}

func insertScheduleExecutionAuditTx(tx *sql.Tx, value storage.OperationAudit) error {
	value = storage.NormalizeOperationAudit(value)
	_, err := tx.Exec(`INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,actor_type,source_component,trigger_type,before_summary,after_summary,status,error_summary,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE status=VALUES(status),after_summary=VALUES(after_summary),error_summary=VALUES(error_summary),updated_at=VALUES(updated_at)`, value.ID, value.InstanceID, value.OperationType, value.TargetType, value.TargetID, value.ActorID, value.ActorType, value.SourceComponent, value.TriggerType, value.BeforeSummary, value.AfterSummary, value.Status, value.ErrorSummary, value.CreatedAt, value.UpdatedAt)
	return err
}

func resolveScheduleAlert(ctx context.Context, tx *sql.Tx, site string, channel int64, now time.Time) error {
	hash := sha256.Sum256([]byte("channel-schedule:" + site + ":" + strconv.FormatInt(channel, 10)))
	_, err := tx.ExecContext(ctx, "UPDATE alerts SET status='resolved',resolved_at=? WHERE id=? AND status<>'resolved'", now, hex.EncodeToString(hash[:20]))
	return err
}

func supersedeScheduleExecutions(ctx context.Context, tx *sql.Tx, site string, channel int64, except, message string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE operation_audits a JOIN channel_schedule_executions e ON e.id=a.id SET a.status='superseded',a.error_summary=?,a.updated_at=? WHERE e.site_id=? AND e.channel_id=? AND e.id<>? AND e.status IN ('pending','retry','running','waiting_recovery','waiting_control')`, message, now, site, channel, except); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE channel_schedule_executions SET status='superseded',error_summary=?,updated_at=? WHERE site_id=? AND channel_id=? AND id<>? AND status IN ('pending','retry','running','waiting_recovery','waiting_control')", message, now, site, channel, except)
	return err
}
