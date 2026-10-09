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

func TestExternalEnableRevokesCircuitOwnership(t *testing.T) {
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
	site := fmt.Sprintf("origin-%d", time.Now().UnixNano())
	_, err = db.Exec(`INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, site, site, site, now, now)
	require.NoError(t, err)
	defer func() {
		for _, table := range []string{"channel_commands", "tuning_recommendations", "operation_audits", "channel_current", "channel_base_values", "tuning_continuous_states", "tuning_policies"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site)
		}
		_, _ = db.Exec("DELETE FROM instances WHERE id=?", site)
	}()
	snapshot := func(status int, at time.Time) {
		t.Helper()
		require.NoError(t, s.StoreFreshChannels(site, []channelcontrol.Channel{{ID: 7, Name: "owned", Models: "m", Weight: 100, Status: status}}, at))
	}
	load := func() tuning.ContinuousState {
		t.Helper()
		states, err := s.ListContinuousStates(site)
		require.NoError(t, err)
		require.Len(t, states, 1)
		return states[0]
	}
	snapshot(1, now)
	policy := tuning.DefaultPolicy()
	policy.DispatchModes = map[string]string{"m": "auto"}
	require.NoError(t, s.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: policy, Mode: "observe", UpdatedAt: now, UpdatedBy: "test"}))
	written := now.Add(time.Second)
	state := tuning.ContinuousState{InstanceID: site, ChannelID: 7, ModelName: "m", CircuitDisabled: true, CircuitStatusTarget: 2, Phase: "probing", LastWriteAt: &written, UpdatedAt: written}
	require.NoError(t, s.PutContinuousState(state))
	// A snapshot while CT is still disabling cannot revoke the pending intent.
	snapshot(1, now.Add(2*time.Second))
	require.True(t, load().CircuitDisabled)
	require.Zero(t, load().ControlRevision)
	// Model a confirmed direct write, then a snapshot captured before that write.
	disabled := 2
	require.NoError(t, s.ApplyChannelWrite(site, 7, nil, nil, &disabled, now.Add(3*time.Second)))
	state.CircuitStatusTarget = 0
	state.LastWriteAt = timePointer(now.Add(3 * time.Second))
	probe := "old-probe"
	state.ProbeCommandID = &probe
	state.ProbeAttempts = 10
	state.ProbeSuccesses = 10
	require.NoError(t, s.PutContinuousState(state))
	snapshot(1, now.Add(2*time.Second))
	require.True(t, load().CircuitDisabled, "old enabled snapshot must not revoke a successful CT disable")
	snapshot(2, now.Add(4*time.Second))
	require.True(t, load().CircuitDisabled, "same disabled snapshot must retain CT origin")
	stale := load()
	// Even with tuning switched off, snapshot ingestion must revoke ownership.
	policy.DispatchModes["m"] = "off"
	require.NoError(t, s.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: policy, Mode: "observe", UpdatedAt: now, UpdatedBy: "test"}))
	snapshot(1, now.Add(5*time.Second))
	reset := load()
	require.False(t, reset.CircuitDisabled)
	require.Equal(t, int64(1), reset.ControlRevision)
	require.Zero(t, reset.CircuitStatusTarget)
	require.Nil(t, reset.ProbeCommandID)
	require.Zero(t, reset.ProbeAttempts)
	require.Equal(t, "normal", reset.Phase)
	require.NoError(t, s.RecordContinuousProbeResult(site, 7, probe, 10, 10, 10, now.Add(6*time.Second)))
	require.Zero(t, load().ProbeAttempts, "late probe must not restore old recovery evidence")
	stale.UpdatedAt = now.Add(time.Minute)
	require.ErrorIs(t, s.PutContinuousState(stale), tuning.ErrDecisionSuperseded, "even a late write must not resurrect the old generation")
	snapshot(1, now.Add(6*time.Second))
	require.Equal(t, int64(1), load().ControlRevision, "repeated enabled snapshots must not repeatedly revoke")
	snapshot(2, now.Add(7*time.Second))
	require.False(t, load().CircuitDisabled, "external disable must remain manually owned")
	rows, err := s.ListChannelBaseValues(site, "")
	require.NoError(t, err)
	require.Empty(t, rows, "manual closure must be excluded from recovery")
	target := 1
	rec := tuning.Recommendation{InstanceID: site, ChannelID: 7, Rule: "circuit_recovered", ModeAtCreation: "auto", ProposedWeight: 20, ProposedChannelStatus: &target}
	require.ErrorIs(t, s.CheckCircuitStatus(rec), ErrCircuitStatusSuperseded)
	// An old recommendation must remain invalid even if a later CT cycle has
	// exactly the same target/weight. A fresh decision carries the new revision.
	snapshot(1, now.Add(8*time.Second))
	current := load()
	current.CircuitDisabled = true
	current.CircuitStatusTarget = 1
	current.ProposedWeight = 20
	require.NoError(t, s.PutContinuousState(current))
	policy.DispatchModes["m"] = "auto"
	require.NoError(t, s.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: policy, Mode: "observe", UpdatedAt: now, UpdatedBy: "test"}))
	require.ErrorIs(t, s.CheckCircuitStatus(rec), ErrCircuitStatusSuperseded)
	rec.Evidence = map[string]any{"control_revision": current.ControlRevision}
	require.NoError(t, s.CheckCircuitStatus(rec))
}

func timePointer(v time.Time) *time.Time { return &v }
