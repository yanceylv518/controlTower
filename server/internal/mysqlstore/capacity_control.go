package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"controltower/server/internal/tuning"
)

var ErrCapacitySuperseded = tuning.ErrDecisionSuperseded

func (s Store) ContinuousCommandResult(id string) (string, time.Time, error) {
	var status string
	var completedAt time.Time
	var reason string
	err := s.db.QueryRowContext(context.Background(), `SELECT status,updated_at,error_summary FROM channel_commands WHERE id=?`, id).Scan(&status, &completedAt, &reason)
	if err == sql.ErrNoRows {
		return "missing", time.Time{}, nil
	}
	if status == "expired" && reason == "capacity decision or mode changed" {
		status = "superseded"
	}
	return status, completedAt, err
}

func capacityManaged(v tuning.Recommendation) bool {
	managed, _ := v.Evidence["capacity_managed"].(bool)
	return managed && v.ProposedChannelStatus == nil && (v.Rule == "weight_write" || v.Rule == "capacity_reduce" || v.Rule == "circuit_opened" || v.Rule == "circuit_recovered")
}

// Both direct writes and Agent claims enforce this gate. A queued request is
// not permission to increase after coverage disappears or the operator changes
// the model mode. Stronger performance decreases remain permitted.
func checkContinuousCapacity(q priorityQuery, v tuning.Recommendation, now time.Time, commandID string) error {
	managed := capacityManaged(v)
	statusRecovery := v.ProposedChannelStatus != nil && *v.ProposedChannelStatus == 1 && v.Rule == "circuit_recovered"
	if !statusRecovery && (v.ProposedChannelStatus != nil || (v.Rule != "weight_write" && v.Rule != "capacity_reduce" && v.Rule != "circuit_opened" && v.Rule != "circuit_recovered")) {
		return nil
	}
	// Legacy untracked decisions cannot be proven current after an upgrade.
	if !managed && !statusRecovery {
		return ErrCapacitySuperseded
	}
	var model, stateModel, phase, paused, raw, policyJSON string
	var baseUpdatedAt, policyUpdatedAt time.Time
	var rpm, tpm, baseWeight int64
	var limited bool
	err := q.QueryRowContext(context.Background(), `SELECT b.model_name,b.base_weight,b.max_rpm,b.max_tpm,s.model_name,s.phase,s.paused_reason,s.capacity_limited,COALESCE(s.capacity_control_json,'{}'),b.updated_at
FROM channel_base_values b JOIN tuning_continuous_states s ON s.instance_id=b.instance_id AND s.channel_id=b.channel_id WHERE b.instance_id=? AND b.channel_id=?`, v.InstanceID, v.ChannelID).Scan(&model, &baseWeight, &rpm, &tpm, &stateModel, &phase, &paused, &limited, &raw, &baseUpdatedAt)
	if err == sql.ErrNoRows {
		return ErrCapacitySuperseded
	}
	if err != nil {
		return err
	}
	var c tuning.CapacityControl
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return err
	}
	if baseWeight <= 0 || stateModel != model || paused == "mixed_channel" {
		return ErrCapacitySuperseded
	}
	// Status ownership/mode is checked by checkCircuitStatus. Re-enabling a
	// disabled channel must still respect the independent capacity interlock.
	if statusRecovery {
		if rpm <= 0 && tpm <= 0 {
			return nil
		}
		fresh := c.Fresh && !c.SampleAt.IsZero() && !c.SampleAt.After(now) && now.Sub(c.SampleAt) <= 90*time.Second
		if limited || c.Active || c.Utilization >= 1 || c.PendingCommandID != "" || !fresh {
			return ErrCapacitySuperseded
		}
		return nil
	}
	var modelsText, channelStatus string
	var onlineWeight int64
	var snapshotAt time.Time
	err = q.QueryRowContext(context.Background(), `SELECT COALESCE(c.models_text,''),c.status,c.weight,c.captured_at FROM channel_current c JOIN instances i ON i.id=c.instance_id
WHERE CASE WHEN i.site_id='' THEN i.id ELSE i.site_id END=? AND i.enabled=1 AND c.channel_id=? ORDER BY c.captured_at DESC,c.instance_id LIMIT 1`, v.InstanceID, v.ChannelID).Scan(&modelsText, &channelStatus, &onlineWeight, &snapshotAt)
	if err == sql.ErrNoRows {
		return ErrCapacitySuperseded
	}
	if err != nil {
		return err
	}
	models := parseChannelModels(modelsText)
	if len(models) != 1 || models[0] != model {
		return ErrCapacitySuperseded
	}
	switch strings.ToLower(channelStatus) {
	case "enabled", "enable", "active", "normal", "1":
	default:
		return ErrCapacitySuperseded
	}
	err = q.QueryRowContext(context.Background(), `SELECT policy_json,updated_at FROM tuning_policies WHERE instance_id=?`, v.InstanceID).Scan(&policyJSON, &policyUpdatedAt)
	if err == sql.ErrNoRows {
		return ErrCapacitySuperseded
	}
	if err != nil {
		return err
	}
	policy, err := tuning.DecodePolicyJSON([]byte(policyJSON))
	if err != nil {
		return err
	}
	if policy.DispatchModes[model] != "auto" {
		return ErrCapacitySuperseded
	}
	var evaluation *tuning.EvaluationContext
	encoded, err := json.Marshal(v.Evidence["evaluation"])
	if err != nil {
		return err
	}
	if err = json.Unmarshal(encoded, &evaluation); err != nil {
		return err
	}
	if evaluation == nil || evaluation.BaseWeight != baseWeight || !evaluation.BaseUpdatedAt.Equal(baseUpdatedAt) || !evaluation.PolicyUpdatedAt.Equal(policyUpdatedAt) || evaluation.Params != policy.Continuous || v.Evidence["model"] != model {
		return ErrCapacitySuperseded
	}
	// A readback/manual update may finish after this tick loaded its bases.
	// Do not compound a new increase from that now-obsolete weight snapshot.
	if snapshotAt.After(evaluation.EvaluatedAt) && onlineWeight != v.CurrentWeight && v.Rule != "circuit_opened" {
		return ErrCapacitySuperseded
	}
	// A circuit zero supersedes an earlier queued capacity decision. Both
	// commands execute on the same Agent; an unclaimed older decision will be
	// expired by its marker check instead of reintroducing traffic.
	if v.Rule == "circuit_opened" {
		if v.ProposedWeight != 0 || (commandID != "" && c.PendingCommandID != commandID) {
			return ErrCapacitySuperseded
		}
		return nil
	}
	if managed {
		var decision tuning.CapacityControl
		encoded, encodeErr := json.Marshal(v.Evidence["capacity"])
		if encodeErr != nil {
			return encodeErr
		}
		if err = json.Unmarshal(encoded, &decision); err != nil {
			return err
		}
		if decision.MaxRPM != rpm || decision.MaxTPM != tpm {
			return ErrCapacitySuperseded
		}
	}
	fresh := rpm <= 0 && tpm <= 0 || c.Fresh && !c.SampleAt.IsZero() && !c.SampleAt.After(now) && now.Sub(c.SampleAt) <= 90*time.Second
	if v.Rule == "circuit_recovered" {
		if (limited && (commandID == "" || c.PendingCommandID != commandID)) || c.Active || c.Utilization >= 1 || (c.PendingCommandID != "" && c.PendingCommandID != commandID) || !fresh {
			return ErrCapacitySuperseded
		}
		return nil
	}
	if !c.Initialized || phase != "normal" || c.MaxRPM != rpm || c.MaxTPM != tpm {
		return ErrCapacitySuperseded
	}
	if commandID == "" {
		if c.PendingCommandID != "" {
			return ErrCapacitySuperseded
		}
	} else if c.PendingCommandID != commandID || c.PendingWeight != v.ProposedWeight {
		return ErrCapacitySuperseded
	}
	if v.ProposedWeight > c.ConfirmedWeight && ((limited && (commandID == "" || c.PendingCommandID != commandID)) || c.Active || c.Utilization >= 1 || !fresh) {
		return ErrCapacitySuperseded
	}
	if v.Rule == "capacity_reduce" && (!fresh || !c.Active || c.Utilization < 1 || c.Reason == "no_eligible_headroom" || c.Reason == "unknown_group" || v.ProposedWeight < 1 || v.ProposedWeight >= c.ConfirmedWeight) {
		return ErrCapacitySuperseded
	}
	return nil
}

func (s Store) CheckContinuousCapacity(v tuning.Recommendation, now time.Time) error {
	return checkContinuousCapacity(s.db, v, now, "")
}

func linkCapacityCommand(tx *sql.Tx, v tuning.Recommendation, commandID string) error {
	if !capacityManaged(v) {
		return nil
	}
	result, err := tx.Exec(`UPDATE tuning_continuous_states SET capacity_control_json=JSON_SET(COALESCE(capacity_control_json,'{}'),'$.pending_command_id',?,'$.pending_weight',?,'$.phase','awaiting_write')
WHERE instance_id=? AND channel_id=? AND (COALESCE(JSON_UNQUOTE(JSON_EXTRACT(capacity_control_json,'$.pending_command_id')),'')='' OR ?)`, commandID, v.ProposedWeight, v.InstanceID, v.ChannelID, v.Rule == "circuit_opened")
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrCapacitySuperseded
	}
	return err
}

// Claim-time validation also covers weight-only soft starts (legacy circuits).
func validateCapacityClaim(tx *sql.Tx, cmdID string, now time.Time) (bool, error) {
	var v tuning.Recommendation
	var evidence string
	var status sql.NullInt64
	err := tx.QueryRow(`SELECT r.instance_id,r.channel_id,r.rule,r.mode_at_creation,r.proposed_weight,r.evidence_json,JSON_EXTRACT(c.payload_json,'$.status'),r.current_weight FROM tuning_recommendations r JOIN channel_commands c ON c.id=r.command_id WHERE r.command_id=? AND r.rule IN ('weight_write','capacity_reduce','circuit_recovered','circuit_opened')`, cmdID).Scan(&v.InstanceID, &v.ChannelID, &v.Rule, &v.ModeAtCreation, &v.ProposedWeight, &evidence, &status, &v.CurrentWeight)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if err = json.Unmarshal([]byte(evidence), &v.Evidence); err != nil {
		return false, err
	}
	if status.Valid {
		value := int(status.Int64)
		v.ProposedChannelStatus = &value
	}
	err = checkContinuousCapacity(tx, v, now, cmdID)
	if !errors.Is(err, ErrCapacitySuperseded) {
		return err == nil, err
	}
	for _, query := range []string{
		`UPDATE channel_commands SET status='expired',error_summary='capacity decision or mode changed',updated_at=? WHERE id=?`,
		`UPDATE operation_audits SET status='expired',error_summary='capacity decision or mode changed',updated_at=? WHERE id=? AND status='submitted'`,
		`UPDATE tuning_recommendations SET status='expired',outcome_at=? WHERE command_id=?`,
	} {
		if _, err = tx.Exec(query, now, cmdID); err != nil {
			return false, err
		}
	}
	return false, nil
}
