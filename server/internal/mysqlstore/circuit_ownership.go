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
// the same transaction as the inventory update. A confirmed CT-disabled channel
// observed enabled has been taken over externally. Do not reset an in-flight
// CT transition: its own write can appear in a snapshot before its receipt.
func reconcileCircuitOwnership(tx *sql.Tx, site string, snapshot storage.ChannelSnapshot) error {
	switch strings.ToLower(strings.TrimSpace(snapshot.Status)) {
	case "enabled", "enable", "active", "normal", "1":
	default:
		return nil
	}
	_, err := tx.Exec(`UPDATE tuning_continuous_states SET
 control_revision=control_revision+1,
 circuit_disabled=0,circuit_status_target=0,circuit_status_command_id='',
 phase='normal',circuit_opened_at=NULL,next_probe_at=NULL,
 probe_command_id=NULL,probe_attempts=0,probe_successes=0,probe_duration_sum=0,probe_slow_streak=0,
 original_priority=NULL,soft_start_pending=0,
 k_error=1,k_speed=1,k_cache=1,k_otps=1,multiplier=1,smoothed_error_rate=0,
 proposed_weight=?,last_written_weight=NULL,last_write_at=NULL,last_observed_weight=?,
 last_bucket_at=?,paused_reason='',write_failure_streak=0,last_write_failure_at=NULL,last_write_error='',
 capacity_control_json='{}',evaluation_json=NULL,updated_at=?
WHERE instance_id=? AND channel_id=? AND circuit_disabled=1 AND circuit_status_target=0
 AND (last_write_at IS NULL OR last_write_at<?)`, snapshot.Weight, snapshot.Weight, snapshot.CapturedAt, snapshot.CapturedAt, site, snapshot.ChannelID, snapshot.CapturedAt)
	return err
}
