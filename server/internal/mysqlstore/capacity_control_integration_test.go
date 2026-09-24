package mysqlstore

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/tuning"
	"github.com/stretchr/testify/require"
)

func TestCapacityStateAndCommandLifecycle(t *testing.T) {
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
	site := fmt.Sprintf("capacity-%d", now.UnixNano())
	_, err = db.Exec(`INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, site, site, site, now, now)
	require.NoError(t, err)
	defer func() {
		for _, table := range []string{"channel_commands", "tuning_recommendations", "operation_audits", "channel_current", "channel_base_values", "tuning_continuous_states", "tuning_policies", "instances"} {
			key := "instance_id"
			if table == "instances" {
				key = "id"
			}
			_, _ = db.Exec("DELETE FROM "+table+" WHERE "+key+"=?", site)
		}
	}()
	require.NoError(t, s.StoreInstanceChannels(site, []channelcontrol.Channel{{ID: 7, Name: "capacity", Models: "m", Weight: 100, Status: 1}}, now))
	_, err = db.Exec(`UPDATE channel_base_values SET max_tpm=1000 WHERE instance_id=? AND channel_id=7`, site)
	require.NoError(t, err)
	policy := tuning.DefaultPolicy()
	policy.DispatchModes = map[string]string{"m": "auto"}
	require.NoError(t, s.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: policy, Mode: "observe", UpdatedAt: now}))
	state := tuning.ContinuousState{InstanceID: site, ChannelID: 7, ModelName: "m", Phase: "normal", ProposedWeight: 75, CapacityLimited: true, MetricTPM: 1500, UpdatedAt: now,
		Capacity: tuning.CapacityControl{Initialized: true, Active: true, Fresh: true, Phase: "reducing", MaxTPM: 1000, Utilization: 1.5, SampleAt: now, OverSince: now.Add(-time.Minute), ConfirmedWeight: 100, RawTarget: 60, BoundWeight: 75}}
	makeRec := func(suffix string) tuning.Recommendation {
		return tuning.Recommendation{ID: site + suffix, InstanceID: site, ChannelID: 7, Rule: "capacity_reduce", ModeAtCreation: "auto", CreatedAt: now, CurrentWeight: 100, ProposedWeight: 75, Evidence: map[string]any{"capacity_managed": true, "capacity": state.Capacity, "model": "m"}}
	}
	require.NoError(t, s.PutContinuousState(state))
	loaded, err := s.ListContinuousStates(site)
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	require.Equal(t, state.Capacity, loaded[0].Capacity)
	id, err := s.CreateContinuousWeightChange(makeRec("first"), "system:auto", now)
	require.NoError(t, err)
	// No engine post-write persistence: the enqueue transaction owns the marker.
	loaded, err = s.ListContinuousStates(site)
	require.NoError(t, err)
	require.Equal(t, id, loaded[0].Capacity.PendingCommandID)
	require.Nil(t, loaded[0].LastWrittenWeight)
	_, err = s.CreateContinuousWeightChange(makeRec("duplicate"), "system:auto", now)
	require.ErrorIs(t, err, ErrCapacitySuperseded)
	commands, err := s.ClaimPendingCommands(site, now.Add(time.Second))
	require.NoError(t, err)
	require.Len(t, commands, 1)
	_, _, err = s.CompleteChannelCommand(id, "succeeded", "", now.Add(2*time.Second))
	require.NoError(t, err)
	status, at, err := s.ContinuousCommandResult(id)
	require.NoError(t, err)
	require.Equal(t, "succeeded", status)
	require.Equal(t, now.Add(2*time.Second), at.UTC())

	for _, reason := range []string{"stale", "mode", "config", "circuit", "peer"} {
		t.Run(reason, func(t *testing.T) {
			policy.DispatchModes["m"] = "auto"
			require.NoError(t, s.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: policy, Mode: "observe", UpdatedAt: now}))
			_, err = db.Exec(`UPDATE channel_base_values SET max_tpm=1000 WHERE instance_id=?`, site)
			require.NoError(t, err)
			require.NoError(t, s.PutContinuousState(state))
			id, err := s.CreateContinuousWeightChange(makeRec(reason), "system:auto", now)
			require.NoError(t, err)
			claimAt := now.Add(time.Second)
			switch reason {
			case "stale":
				claimAt = now.Add(91 * time.Second)
			case "mode":
				policy.DispatchModes["m"] = "observe"
				require.NoError(t, s.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: policy, Mode: "observe", UpdatedAt: now}))
			case "config":
				_, err = db.Exec(`UPDATE channel_base_values SET max_tpm=2000 WHERE instance_id=?`, site)
				require.NoError(t, err)
			case "circuit":
				_, err = db.Exec(`UPDATE tuning_continuous_states SET phase='circuit' WHERE instance_id=?`, site)
				require.NoError(t, err)
			case "peer":
				_, err = db.Exec(`UPDATE tuning_continuous_states SET capacity_control_json=JSON_SET(capacity_control_json,'$.reason','no_eligible_headroom') WHERE instance_id=?`, site)
				require.NoError(t, err)
			}
			commands, err := s.ClaimPendingCommands(site, claimAt)
			require.NoError(t, err)
			require.Empty(t, commands)
			status, _, err := s.ContinuousCommandResult(id)
			require.NoError(t, err)
			require.Equal(t, "superseded", status)
			expired, err := s.HasExpiredAutoCommands(site, now.Add(-time.Minute))
			require.NoError(t, err)
			require.False(t, expired, "intentional cancellation must not stop all automatic models")
		})
	}
	policy.DispatchModes["m"] = "auto"
	require.NoError(t, s.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: policy, Mode: "observe", UpdatedAt: now}))
	_, err = db.Exec(`UPDATE channel_base_values SET max_tpm=1000 WHERE instance_id=?`, site)
	require.NoError(t, err)
	require.NoError(t, s.PutContinuousState(state))
	_, err = s.CreateContinuousWeightChange(makeRec("beforecircuit"), "system:auto", now)
	require.NoError(t, err)
	state.Phase = "circuit"
	state.ProposedWeight = 0
	// Keep the pending marker when opening a circuit, as the engine does.
	loaded, err = s.ListContinuousStates(site)
	require.NoError(t, err)
	state.Capacity.PendingCommandID = loaded[0].Capacity.PendingCommandID
	require.NoError(t, s.PutContinuousState(state))
	rec := makeRec("zero")
	rec.Rule = "circuit_opened"
	rec.ProposedWeight = 0
	id, err = s.CreateContinuousWeightChange(rec, "system:auto", now)
	require.NoError(t, err)
	commands, err = s.ClaimPendingCommands(site, now)
	require.NoError(t, err)
	require.Len(t, commands, 1)
	require.Equal(t, id, commands[0].ID)
	_, _, err = s.CompleteChannelCommand(id, "succeeded", "", now)
	require.NoError(t, err)
	// A healthy increase remains executable after the engine marks its own
	// outstanding command as limited. That flag must not cancel every queue write.
	state.Phase = "normal"
	state.CapacityLimited = false
	state.Capacity.Active = false
	state.Capacity.Utilization = .5
	state.Capacity.PendingCommandID = ""
	state.Capacity.ConfirmedWeight = 100
	state.ProposedWeight = 110
	require.NoError(t, s.PutContinuousState(state))
	rec = makeRec("increase")
	rec.Rule = "weight_write"
	rec.ProposedWeight = 110
	id, err = s.CreateContinuousWeightChange(rec, "system:auto", now)
	require.NoError(t, err)
	loaded, err = s.ListContinuousStates(site)
	require.NoError(t, err)
	loaded[0].CapacityLimited = true
	require.NoError(t, s.PutContinuousState(loaded[0]))
	commands, err = s.ClaimPendingCommands(site, now.Add(time.Second))
	require.NoError(t, err)
	require.Len(t, commands, 1)
	require.Equal(t, id, commands[0].ID)
	_, _, err = s.CompleteChannelCommand(id, "succeeded", "", now)
	require.NoError(t, err)
	// Direct writes persist the same terminal marker even if the process exits
	// before the engine updates its in-memory state.
	require.NoError(t, s.PutContinuousState(state))
	rec = makeRec("direct")
	rec.Rule = "weight_write"
	rec.ProposedWeight = 110
	require.NoError(t, s.CheckContinuousCapacity(rec, now))
	id, err = s.RecordDirectWeightChange(rec, "system:auto", now.Add(2*time.Second))
	require.NoError(t, err)
	loaded, err = s.ListContinuousStates(site)
	require.NoError(t, err)
	require.Equal(t, id, loaded[0].Capacity.PendingCommandID)
	status, at, err = s.ContinuousCommandResult(id)
	require.NoError(t, err)
	require.Equal(t, "succeeded", status)
	require.Equal(t, now.Add(2*time.Second), at.UTC())
}
