package mysqlstore

import (
	"database/sql"
	"encoding/json"
	"strings"

	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
)

// The revision is CT-local. It is never sent to NewAPI.
func recommendationControlRevision(v tuning.Recommendation) int64 {
	raw, err := json.Marshal(v.Evidence)
	if err != nil {
		return -1
	}
	var evidence struct {
		Revision int64 `json:"control_revision"`
	}
	if json.Unmarshal(raw, &evidence) != nil {
		return -1
	}
	return evidence.Revision // Existing decisions belong to generation zero.
}

// Called only after a snapshot passes the source and captured_at guards, in
// the same transaction as the inventory update. NewAPI enabling a previously
// disabled channel releases both circuit ownership and explicit manual control.
// An enabled snapshot alone cannot release a failed/unconfirmed manual close:
// require a confirmed close or a disabled-to-enabled transition, and no pending
// CT manual write. last_write_at is cleared whenever a new manual action begins.
func reconcileChannelOwnership(tx *sql.Tx, site string, snapshot storage.ChannelSnapshot, previousStatus string) error {
	switch strings.ToLower(strings.TrimSpace(snapshot.Status)) {
	case "enabled", "enable", "active", "normal", "1":
	default:
		return nil
	}
	previouslyDisabled := false
	switch strings.ToLower(strings.TrimSpace(previousStatus)) {
	case "disabled", "disable", "inactive", "2", "3", "auto_disabled", "auto-disabled":
		previouslyDisabled = true
	}
	if _, err := tx.Exec(`UPDATE channel_schedules s SET closed_by_schedule=0 WHERE site_id=? AND channel_id=? AND closed_by_schedule=1 AND NOT EXISTS (SELECT 1 FROM channel_schedule_executions e WHERE e.id=s.last_execution_id AND e.status='running')`, site, snapshot.ChannelID); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE tuning_continuous_states SET
 control_revision=control_revision+1,
 circuit_disabled=0,routine_circuit_latched=0,circuit_status_target=0,circuit_status_command_id='',
 phase='normal',circuit_opened_at=NULL,next_probe_at=NULL,
 probe_command_id=NULL,last_probe_command_id=NULL,probe_attempts=0,probe_successes=0,probe_duration_sum=0,probe_slow_streak=0,
 original_priority=NULL,soft_start_pending=0,
 k_error=1,k_speed=1,k_cache=1,k_otps=1,multiplier=1,smoothed_error_rate=0,
 proposed_weight=?,last_written_weight=NULL,last_write_at=NULL,last_observed_weight=?,
 last_bucket_at=?,paused_reason='',write_failure_streak=0,last_write_failure_at=NULL,last_write_error='',
 capacity_control_json='{}',evaluation_json=NULL,probe_speed_json=NULL,updated_at=?
WHERE instance_id=? AND channel_id=? AND (
 (circuit_disabled=1 AND circuit_status_target=0 AND (last_write_at IS NULL OR last_write_at<?))
 OR (paused_reason='manual_disabled' AND (? OR last_write_at IS NOT NULL) AND updated_at<?
  AND NOT EXISTS (SELECT 1 FROM channel_commands c JOIN instances i ON i.id=c.instance_id
   WHERE COALESCE(NULLIF(i.site_id,''),i.id)=? AND c.channel_id=? AND c.command_type='channel.update'
    AND c.status='delivered' AND JSON_EXTRACT(c.payload_json,'$.manual_control')=true
    AND JSON_EXTRACT(c.payload_json,'$.control_revision')=tuning_continuous_states.control_revision))
)`, snapshot.Weight, snapshot.Weight, snapshot.CapturedAt, snapshot.CapturedAt, site, snapshot.ChannelID, snapshot.CapturedAt, previouslyDisabled, snapshot.CapturedAt, site, snapshot.ChannelID)
	return err
}
