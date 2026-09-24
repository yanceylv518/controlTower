package mysqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/tuning"
	"github.com/stretchr/testify/require"
)

func TestCircuitDisabledChannelRemainsEligibleAndStatusCommandsPersist(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated CT_MYSQL_TEST_DSN")
	}
	db, err := Open(dsn)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, ApplyDir(context.Background(), db, "../../migrations"))
	s := New(db)
	now := time.Now().UTC().Truncate(time.Second)
	site := fmt.Sprintf("circuit-status-%d", time.Now().UnixNano())
	_, err = db.Exec(`INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, site, site, site, now, now)
	require.NoError(t, err)
	defer func() {
		for _, table := range []string{"channel_commands", "tuning_recommendations", "operation_audits", "channel_current", "channel_base_values", "tuning_continuous_states", "tuning_policies"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site)
		}
		_, _ = db.Exec("DELETE FROM instances WHERE id=?", site)
	}()
	require.NoError(t, s.StoreInstanceChannels(site, []channelcontrol.Channel{{ID: 7, Name: "owned", Models: "m", Weight: 100, Status: 1}, {ID: 8, Name: "manual", Models: "m", Weight: 100, Status: 1}}, now))
	disabled := 2
	require.NoError(t, s.ApplyChannelWrite(site, 7, nil, nil, &disabled, now))
	require.NoError(t, s.ApplyChannelWrite(site, 8, nil, nil, &disabled, now))
	rows, err := s.ListChannelBaseValues(site, "")
	require.NoError(t, err)
	require.Empty(t, rows)
	state := tuning.ContinuousState{InstanceID: site, ChannelID: 7, ModelName: "m", Phase: "probing", CircuitDisabled: true, CircuitStatusTarget: 2, UpdatedAt: now}
	require.NoError(t, s.PutContinuousState(state))
	rows, err = s.ListChannelBaseValues(site, "")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(7), rows[0].ChannelID)
	policy := tuning.DefaultPolicy()
	policy.DispatchModes = map[string]string{"m": "auto"}
	require.NoError(t, s.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: policy, Mode: "observe", UpdatedAt: now, UpdatedBy: "test"}))
	for _, target := range []int{2, 1} {
		weight := int64(0)
		rule := "circuit_disabled"
		if target == 1 {
			weight = 20
			rule = "circuit_recovered"
		}
		rec := tuning.Recommendation{ID: fmt.Sprintf("%s-%d", site, target), InstanceID: site, ChannelID: 7, Rule: rule, ModeAtCreation: "auto", CreatedAt: now, ProposedWeight: weight, ProposedChannelStatus: &target, Evidence: map[string]any{"channel_status": target}}
		state.CircuitStatusTarget, state.ProposedWeight = target, weight
		require.NoError(t, s.PutContinuousState(state))
		id, err := s.CreateContinuousWeightChange(rec, "system:auto", now)
		require.NoError(t, err)
		states, err := s.ListContinuousStates(site)
		require.NoError(t, err)
		require.Equal(t, id, states[0].CircuitStatusCommandID)
		require.Equal(t, target, states[0].CircuitStatusTarget)
		status, err := s.ContinuousCommandStatus(id)
		require.NoError(t, err)
		require.Equal(t, "pending", status)
		cmds, err := s.ClaimPendingCommands(site, now)
		require.NoError(t, err)
		require.Len(t, cmds, 1)
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(cmds[0].PayloadJSON), &payload))
		require.Equal(t, float64(target), payload["status"])
		require.Equal(t, float64(weight), payload["weight"])
		_, _, err = s.CompleteChannelCommand(id, "succeeded", "", now)
		require.NoError(t, err)
		status, err = s.ContinuousCommandStatus(id)
		require.NoError(t, err)
		require.Equal(t, "succeeded", status)
		var channelStatus string
		require.NoError(t, db.QueryRow(`SELECT status FROM channel_current WHERE instance_id=? AND channel_id=7`, site).Scan(&channelStatus))
		if target == 2 {
			require.Equal(t, "disabled", channelStatus)
		} else {
			require.Equal(t, "enabled", channelStatus)
		}
	}
	for _, mode := range []string{"off", "observe"} {
		policy.DispatchModes["m"] = "auto"
		require.NoError(t, s.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: policy, Mode: "observe", UpdatedAt: now, UpdatedBy: "test"}))
		state.CircuitStatusTarget, state.ProposedWeight = 2, 0
		require.NoError(t, s.PutContinuousState(state))
		rec := tuning.Recommendation{ID: site + "-" + mode, InstanceID: site, ChannelID: 7, Rule: "circuit_disabled", ModeAtCreation: "auto", CreatedAt: now, ProposedChannelStatus: &disabled}
		id, err := s.CreateContinuousWeightChange(rec, "system:auto", now)
		require.NoError(t, err)
		policy.DispatchModes["m"] = mode
		require.NoError(t, s.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: policy, Mode: "observe", UpdatedAt: now, UpdatedBy: "test"}))
		cmds, err := s.ClaimPendingCommands(site, now)
		require.NoError(t, err)
		require.Empty(t, cmds)
		status, err := s.ContinuousCommandStatus(id)
		require.NoError(t, err)
		require.Equal(t, "expired", status)
	}
	probe, err := s.CreateContinuousProbe(tuning.Recommendation{ID: site + "-probe", InstanceID: site, ChannelID: 7, CreatedAt: now, ModeAtCreation: "auto"}, "m", 10, 1, now)
	require.NoError(t, err)
	state.ProbeCommandID = &probe
	require.NoError(t, s.PutContinuousState(state))
	require.NoError(t, s.RecordContinuousProbeResult(site, 7, probe, 5, 0, 0, now))
	expected, err := s.CompletedProbeCount(site, 7)
	require.NoError(t, err)
	require.Equal(t, 10, expected, "partial round must retain the original dispatched count")
}
