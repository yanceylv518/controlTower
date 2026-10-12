package mysqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"controltower/server/internal/channelupdates"
	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
)

// Serialize CT status/weight writes across API and scheduler processes. The
// connection remains checked out until RELEASE_LOCK, not returned to the pool.
func (s Store) LockChannelWrite(ctx context.Context, site string, channel int64) (func(), error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("ct-channel:%x", sha256.Sum256([]byte(fmt.Sprintf("%s/%d", site, channel))))[:64]
	var acquired sql.NullInt64
	if err = conn.QueryRowContext(ctx, `SELECT GET_LOCK(?,10)`, key).Scan(&acquired); err != nil || !acquired.Valid || acquired.Int64 != 1 {
		if err != nil {
			// A lost response may hide a successful acquisition. Never return
			// that session (and its advisory lock) to the connection pool.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		conn.Close()
		if err != nil {
			return nil, err
		}
		return nil, tuning.ErrChannelControlBusy
	}
	return func() {
		release, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(release, `DO RELEASE_LOCK(?)`, key); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		conn.Close()
	}, nil
}

func checkManualChannelControl(q priorityQuery, v tuning.Recommendation) error {
	var paused string
	var revision int64
	err := q.QueryRowContext(context.Background(), `SELECT paused_reason,control_revision FROM tuning_continuous_states WHERE instance_id=? AND channel_id=?`, v.InstanceID, v.ChannelID).Scan(&paused, &revision)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if paused == tuning.ManualDisabled || (v.Rule != "base_priority_sync" && revision != recommendationControlRevision(v)) {
		return tuning.ErrDecisionSuperseded
	}
	return nil
}

func (s Store) CheckManualChannelControl(v tuning.Recommendation) error {
	return checkManualChannelControl(s.db, v)
}

// Fence old evaluations before contacting NewAPI. Failure/unknown outcomes keep
// the hold; only a confirmed explicit enable releases it.
func (s Store) BeginManualChannelStatus(ctx context.Context, site string, channel int64, target int, actor string, now time.Time) (storage.ChannelCommand, error) {
	if target != 1 && target != 2 {
		return storage.ChannelCommand{}, fmt.Errorf("invalid channel status")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storage.ChannelCommand{}, err
	}
	defer tx.Rollback()
	// Lock queued writes before checking their state so an Agent cannot claim
	// an old recovery between the ownership check and pending-command expiry.
	commands, err := tx.QueryContext(ctx, `SELECT c.status,c.command_type,c.created_by FROM channel_commands c JOIN instances i ON i.id=c.instance_id WHERE COALESCE(NULLIF(i.site_id,''),i.id)=? AND c.channel_id=? AND c.status IN ('pending','delivered') AND COALESCE(JSON_EXTRACT(c.payload_json,'$.manual_control'),false)=false FOR UPDATE`, site, channel)
	if err != nil {
		return storage.ChannelCommand{}, err
	}
	busy := false
	for commands.Next() {
		var status, kind, author string
		if err = commands.Scan(&status, &kind, &author); err != nil {
			commands.Close()
			return storage.ChannelCommand{}, err
		}
		busy = busy || (kind == "channel.update" && (status == "delivered" || author != "system:auto"))
	}
	err = commands.Err()
	commands.Close()
	if err != nil {
		return storage.ChannelCommand{}, err
	}
	if busy {
		return storage.ChannelCommand{}, tuning.ErrChannelControlBusy
	}
	var previous string
	var weight int64
	err = tx.QueryRowContext(ctx, `SELECT c.status,c.weight FROM channel_current c JOIN instances i ON i.id=c.instance_id WHERE i.enabled=1 AND COALESCE(NULLIF(i.site_id,''),i.id)=? AND c.channel_id=? ORDER BY c.captured_at DESC,i.id LIMIT 1 FOR UPDATE`, site, channel).Scan(&previous, &weight)
	if err == sql.ErrNoRows {
		return storage.ChannelCommand{}, tuning.ErrChannelNotFound
	}
	if err != nil {
		return storage.ChannelCommand{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO tuning_continuous_states(instance_id,channel_id,model_name,updated_at) VALUES(?,?,COALESCE((SELECT model_name FROM channel_base_values WHERE instance_id=? AND channel_id=?),''),?) ON DUPLICATE KEY UPDATE instance_id=VALUES(instance_id)`, site, channel, site, channel, now)
	if err != nil {
		return storage.ChannelCommand{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE tuning_continuous_states SET control_revision=control_revision+1,paused_reason=?,phase='normal',
circuit_disabled=0,routine_circuit_latched=0,circuit_status_target=0,circuit_status_command_id='',circuit_opened_at=NULL,next_probe_at=NULL,
probe_command_id=NULL,last_probe_command_id=NULL,probe_attempts=0,probe_successes=0,probe_duration_sum=0,probe_slow_streak=0,
original_priority=NULL,soft_start_pending=0,k_error=1,k_speed=1,k_cache=1,k_otps=1,multiplier=1,smoothed_error_rate=0,
proposed_weight=?,last_written_weight=NULL,last_write_at=NULL,last_observed_weight=?,last_bucket_at=?,
write_failure_streak=0,last_write_failure_at=NULL,last_write_error='',capacity_control_json='{}',evaluation_json=NULL,probe_speed_json=NULL,updated_at=?
WHERE instance_id=? AND channel_id=?`, tuning.ManualDisabled, weight, weight, now, now, site, channel)
	if err != nil {
		return storage.ChannelCommand{}, err
	}
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT control_revision FROM tuning_continuous_states WHERE instance_id=? AND channel_id=?`, site, channel).Scan(&revision); err != nil {
		return storage.ChannelCommand{}, err
	}
	for _, query := range []string{
		`UPDATE operation_audits a JOIN channel_commands c ON c.id=a.id JOIN instances i ON i.id=c.instance_id SET a.status='expired',a.error_summary='manual channel control',a.updated_at=? WHERE COALESCE(NULLIF(i.site_id,''),i.id)=? AND c.channel_id=? AND c.created_by='system:auto' AND c.status='pending' AND a.status='submitted'`,
		`UPDATE tuning_recommendations r JOIN channel_commands c ON c.id=r.command_id JOIN instances i ON i.id=c.instance_id SET r.status='expired',r.outcome_at=? WHERE COALESCE(NULLIF(i.site_id,''),i.id)=? AND c.channel_id=? AND c.created_by='system:auto' AND c.status='pending'`,
	} {
		if _, err = tx.ExecContext(ctx, query, now, site, channel); err != nil {
			return storage.ChannelCommand{}, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE channel_commands c JOIN instances i ON i.id=c.instance_id SET c.status='expired',c.error_summary='manual channel control',c.updated_at=? WHERE COALESCE(NULLIF(i.site_id,''),i.id)=? AND c.channel_id=? AND c.created_by='system:auto' AND c.status='pending'`, now, site, channel)
	if err != nil {
		return storage.ChannelCommand{}, err
	}
	instance, err := controlInstanceForSite(tx, site)
	if err != nil {
		return storage.ChannelCommand{}, err
	}
	payload, _ := json.Marshal(map[string]any{"status": target, "manual_control": true, "control_revision": revision})
	cmd := storage.ChannelCommand{ID: randomCommandID(), InstanceID: instance, ChannelID: channel, CommandType: "channel.update", PayloadJSON: string(payload), Status: "delivered", CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
	_, err = tx.ExecContext(ctx, `INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,error_summary,created_at,updated_at) VALUES(?,?,?,?,?,?,?,'',?,?)`, cmd.ID, instance, channel, cmd.CommandType, cmd.PayloadJSON, cmd.Status, actor, now, now)
	if err != nil {
		return storage.ChannelCommand{}, err
	}
	before, _ := json.Marshal(map[string]any{"status": previous})
	after, _ := json.Marshal(map[string]any{"status": target, "manual_control": true, "command_id": cmd.ID})
	err = insertOperationAuditTx(tx, storage.OperationAudit{ID: cmd.ID, InstanceID: site, OperationType: "channel.update", TargetType: "channel", TargetID: fmt.Sprint(channel), ActorID: actor, CorrelationID: cmd.ID, BeforeSummary: string(before), AfterSummary: string(after), Status: "submitted", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return storage.ChannelCommand{}, err
	}
	if err = tx.Commit(); err != nil {
		return storage.ChannelCommand{}, err
	}
	channelupdates.Notify(site)
	return cmd, nil
}

func (s Store) FinishManualChannelStatus(site string, cmd storage.ChannelCommand, succeeded bool, at time.Time) error {
	var payload struct {
		Status   int   `json:"status"`
		Revision int64 `json:"control_revision"`
	}
	if err := json.Unmarshal([]byte(cmd.PayloadJSON), &payload); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var recorded string
	if err = tx.QueryRow(`SELECT status FROM channel_commands WHERE id=?`, cmd.ID).Scan(&recorded); err != nil {
		return err
	}
	if (succeeded && recorded == "succeeded") || (!succeeded && recorded == "failed") {
		return nil
	}
	var revision int64
	if err = tx.QueryRow(`SELECT control_revision FROM tuning_continuous_states WHERE instance_id=? AND channel_id=? FOR UPDATE`, site, cmd.ChannelID).Scan(&revision); err != nil {
		return err
	}
	if revision != payload.Revision {
		return tuning.ErrDecisionSuperseded
	}
	status, reason := "failed", "status request unconfirmed; manual hold retained"
	if succeeded {
		status, reason = "succeeded", ""
		if err = applyCompletedChannelWrite(tx, cmd, at); err != nil {
			return err
		}
		if payload.Status == 1 {
			// Advance again so even an evaluation that read the held generation
			// cannot restore it after this explicit enable.
			_, err = tx.Exec(`UPDATE tuning_continuous_states SET control_revision=control_revision+1,paused_reason='',last_bucket_at=?,updated_at=? WHERE instance_id=? AND channel_id=?`, at, at, site, cmd.ChannelID)
			if err != nil {
				return err
			}
		} else {
			// Keep proof of a confirmed close even if the channel directory is
			// rebuilt or its collector changes before a later external enable.
			if _, err = tx.Exec(`UPDATE tuning_continuous_states SET last_write_at=?,updated_at=? WHERE instance_id=? AND channel_id=?`, at, at, site, cmd.ChannelID); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(`UPDATE channel_commands SET status=?,error_summary=?,updated_at=? WHERE id=?`, status, reason, at, cmd.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE operation_audits SET status=?,error_summary=?,updated_at=? WHERE id=?`, status, reason, at, cmd.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		channelupdates.Notify(site)
	}
	return err
}

func (s Store) ContinuousProbeActive(site string, channel int64, command string) (bool, error) {
	var active bool
	err := s.db.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM tuning_continuous_states WHERE instance_id=? AND channel_id=? AND probe_command_id=? AND paused_reason<>?)`, site, channel, command, tuning.ManualDisabled).Scan(&active)
	return active, err
}
