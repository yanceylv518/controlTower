package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"

	"controltower/server/internal/tuning"
)

var ErrCircuitStatusSuperseded = errors.New("circuit status target or automatic mode changed")

// Only the new circuit-owned status writes are gated here. Ordinary weight,
// manual channel operations and priority synchronization retain their contracts.
func checkCircuitStatus(q priorityQuery, v tuning.Recommendation) error {
	if v.ProposedChannelStatus == nil {
		return nil
	}
	target := *v.ProposedChannelStatus
	if (target != 1 && target != 2) || (target == 1 && v.Rule != "circuit_recovered") || (target == 2 && v.Rule != "circuit_disabled") || v.ModeAtCreation != "auto" {
		return ErrCircuitStatusSuperseded
	}
	var model, stateModel string
	var weight, proposedWeight int64
	var owned bool
	var pending int
	err := q.QueryRowContext(context.Background(), `SELECT b.model_name,b.base_weight,s.model_name,s.circuit_disabled,s.circuit_status_target,s.proposed_weight
FROM channel_base_values b JOIN tuning_continuous_states s ON s.instance_id=b.instance_id AND s.channel_id=b.channel_id
WHERE b.instance_id=? AND b.channel_id=?`, v.InstanceID, v.ChannelID).Scan(&model, &weight, &stateModel, &owned, &pending, &proposedWeight)
	if err == sql.ErrNoRows {
		return ErrCircuitStatusSuperseded
	}
	if err != nil {
		return err
	}
	if !owned || pending != target || weight <= 0 || model != stateModel || proposedWeight != v.ProposedWeight {
		return ErrCircuitStatusSuperseded
	}
	var raw string
	err = q.QueryRowContext(context.Background(), `SELECT policy_json FROM tuning_policies WHERE instance_id=?`, v.InstanceID).Scan(&raw)
	if err == sql.ErrNoRows {
		return ErrCircuitStatusSuperseded
	}
	if err != nil {
		return err
	}
	policy, err := tuning.DecodePolicyJSON([]byte(raw))
	if err != nil {
		return err
	}
	if target == 1 && v.ProposedWeight != max(int64(1), int64(math.Round(float64(weight)*policy.Continuous.SoftStartMultiplier))) {
		return ErrCircuitStatusSuperseded
	}
	if policy.DispatchModes[model] != "auto" {
		return ErrCircuitStatusSuperseded
	}
	var modelsText string
	err = q.QueryRowContext(context.Background(), `SELECT c.models_text FROM channel_current c JOIN instances i ON i.id=c.instance_id
WHERE CASE WHEN i.site_id='' THEN i.id ELSE i.site_id END=? AND i.enabled=1 AND c.channel_id=? ORDER BY c.captured_at DESC,c.instance_id LIMIT 1`, v.InstanceID, v.ChannelID).Scan(&modelsText)
	if err == sql.ErrNoRows {
		return ErrCircuitStatusSuperseded
	}
	if err != nil {
		return err
	}
	models := parseChannelModels(modelsText)
	if len(models) != 1 || models[0] != model {
		return ErrCircuitStatusSuperseded
	}
	return nil
}

func (s Store) CheckCircuitStatus(v tuning.Recommendation) error { return checkCircuitStatus(s.db, v) }

func (s Store) CompletedProbeCount(site string, channel int64) (int, error) {
	var payload string
	err := s.db.QueryRowContext(context.Background(), `SELECT c.payload_json FROM tuning_continuous_states s
JOIN channel_commands c ON c.id=s.last_probe_command_id AND c.channel_id=s.channel_id AND c.command_type='channel.probe'
WHERE s.instance_id=? AND s.channel_id=?`, site, channel).Scan(&payload)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var round struct {
		Count int `json:"probe_count"`
	}
	err = json.Unmarshal([]byte(payload), &round)
	return round.Count, err
}
