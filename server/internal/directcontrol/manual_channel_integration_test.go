package directcontrol

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/mysqlstore"
	"controltower/server/internal/secrets"
	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
	"github.com/stretchr/testify/require"
)

func manualFixture(t *testing.T) (*sql.DB, mysqlstore.Store, Store, *fakeController, string, time.Time) {
	t.Helper()
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated CT_MYSQL_TEST_DSN")
	}
	db, err := mysqlstore.Open(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, mysqlstore.ApplyDir(context.Background(), db, "../../migrations"))
	inner := mysqlstore.New(db)
	now := time.Now().UTC().Truncate(time.Second)
	site := fmt.Sprintf("manual-%d", time.Now().UnixNano())
	require.NoError(t, inner.CreateInstance(storage.Instance{ID: site, SiteID: site, Name: site, Enabled: true, CreatedAt: now, UpdatedAt: now}))
	t.Cleanup(func() {
		for _, table := range []string{"channel_control_requests", "channel_commands", "tuning_recommendations", "operation_audits", "channel_current", "channel_base_values", "tuning_continuous_states", "tuning_policies"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site)
		}
		_, _ = db.Exec("DELETE FROM instances WHERE id=?", site)
	})
	encrypted, err := secrets.Encrypt("test-key", "test-token")
	require.NoError(t, err)
	require.NoError(t, inner.UpdateControlConfigForSite(site, "http://test.invalid", encrypted, 7, now))
	require.NoError(t, inner.StoreFreshChannels(site, []channelcontrol.Channel{{ID: 7, Name: "test", Models: "m", Weight: 100, Status: 1}}, now))
	policy := tuning.DefaultPolicy()
	policy.DispatchModes = map[string]string{"m": "auto"}
	require.NoError(t, inner.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: policy, Mode: "observe", UpdatedAt: now, UpdatedBy: "test"}))
	f := &fakeController{}
	direct := Wrap(inner, "test-key").WithFactory(func(string, string, int64) Controller { return f }, nil)
	return db, inner, direct, f, site, now
}

func manualState(t *testing.T, inner mysqlstore.Store, site string) tuning.ContinuousState {
	t.Helper()
	states, err := inner.ListContinuousStates(site)
	require.NoError(t, err)
	require.Len(t, states, 1)
	return states[0]
}

func TestManualChannelCloseTakesOwnershipAndExplicitEnableReleasesIt(t *testing.T) {
	for _, origin := range []string{"enabled", "auto_disabled", "enabling"} {
		t.Run(origin, func(t *testing.T) {
			db, inner, direct, f, site, now := manualFixture(t)
			oldProbe := "old-probe"
			old := tuning.ContinuousState{InstanceID: site, ChannelID: 7, ModelName: "m", Phase: "normal", UpdatedAt: now}
			if origin != "enabled" {
				old.Phase = "probing"
				old.CircuitDisabled = true
				old.ProbeCommandID = &oldProbe
				if origin == "enabling" {
					old.CircuitStatusTarget = 1
					old.ProposedWeight = 20
				}
			}
			require.NoError(t, inner.PutContinuousState(old))
			if origin != "enabled" {
				status := 2
				require.NoError(t, inner.ApplyChannelWrite(site, 7, nil, nil, &status, now))
			}
			cmd, err := direct.UpdateChannelStatus(context.Background(), site, 7, 2, "user:7", now)
			require.NoError(t, err)
			require.Equal(t, "succeeded", cmd.Status)
			held := manualState(t, inner, site)
			require.Equal(t, tuning.ManualDisabled, held.PausedReason)
			require.False(t, held.CircuitDisabled)
			require.Nil(t, held.ProbeCommandID)
			require.Zero(t, held.CircuitStatusTarget)
			require.Greater(t, held.ControlRevision, old.ControlRevision)
			require.ErrorIs(t, inner.PutContinuousState(old), tuning.ErrDecisionSuperseded)
			require.NoError(t, inner.RecordContinuousProbeResult(site, 7, oldProbe, 10, 10, 1, now))
			require.Equal(t, held, manualState(t, inner, site))
			bases, err := inner.ListChannelBaseValues(site, "")
			require.NoError(t, err)
			require.Empty(t, bases)
			on := 1
			priority := int64(3)
			for _, rec := range []tuning.Recommendation{
				{Rule: "circuit_recovered", ProposedChannelStatus: &on, ProposedWeight: 20},
				{Rule: "weight_write", ProposedWeight: 100},
				{Rule: "base_priority_sync", ProposedPriority: &priority},
			} {
				rec.InstanceID, rec.ChannelID, rec.ModeAtCreation = site, 7, "auto"
				rec.Evidence = map[string]any{"control_revision": held.ControlRevision}
				_, err = direct.CreateContinuousWeightChange(rec, "system:auto", now)
				require.ErrorIs(t, err, tuning.ErrDecisionSuperseded)
			}
			_, err = direct.CreateContinuousProbe(tuning.Recommendation{InstanceID: site, ChannelID: 7, Evidence: map[string]any{"control_revision": held.ControlRevision}}, "m", 10, 0, now)
			require.ErrorIs(t, err, tuning.ErrDecisionSuperseded)
			require.Len(t, f.updates, 1)
			// Fresh snapshots, a restart/new store and model edits cannot drop a channel-wide hold.
			for _, models := range []string{"n", "n,other", "m"} {
				require.NoError(t, inner.StoreFreshChannels(site, []channelcontrol.Channel{{ID: 7, Name: "test", Models: models, Weight: 100, Status: 2}}, time.Now().UTC()))
			}
			require.Equal(t, tuning.ManualDisabled, manualState(t, mysqlstore.New(db), site).PausedReason)
			cmd, err = direct.UpdateChannelStatus(context.Background(), site, 7, 1, "user:7", time.Now().UTC())
			require.NoError(t, err)
			require.Equal(t, "succeeded", cmd.Status)
			active := manualState(t, inner, site)
			require.Empty(t, active.PausedReason)
			require.False(t, active.CircuitDisabled)
			require.Greater(t, active.ControlRevision, held.ControlRevision)
			bases, err = inner.ListChannelBaseValues(site, "")
			require.NoError(t, err)
			require.Len(t, bases, 1)
			require.Equal(t, int64(100), bases[0].CurrentWeight)
			require.Len(t, f.updates, 2)
			require.Nil(t, f.updates[1].Weight, "explicit enable preserves online weight")
			require.ErrorIs(t, inner.PutContinuousState(held), tuning.ErrDecisionSuperseded)
			life, err := inner.QueryChannelLifeline(context.Background(), tuning.ProbeRecordQuery{Site: site, ChannelID: 7, From: now.Add(-time.Minute), To: time.Now().Add(time.Minute), Now: time.Now(), Limit: 50}, "operations")
			require.NoError(t, err)
			require.Len(t, life.Items, 2)
			require.Equal(t, "channel.manual_enable", life.Items[0].Operation)
			require.Equal(t, "channel.manual_disable", life.Items[1].Operation)
		})
	}
}

func TestManualChannelUnconfirmedRequestKeepsDurableHold(t *testing.T) {
	_, inner, direct, f, site, now := manualFixture(t)
	f.err = context.DeadlineExceeded
	cmd, err := direct.UpdateChannelStatus(context.Background(), site, 7, 2, "operator", now)
	require.Error(t, err)
	require.NotEmpty(t, cmd.ID)
	require.Equal(t, tuning.ManualDisabled, manualState(t, inner, site).PausedReason)
	status, err := inner.ContinuousCommandStatus(cmd.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", status)
	_, err = direct.UpdateChannelStatus(context.Background(), site, 7, 1, "operator", now)
	require.Error(t, err)
	require.Equal(t, tuning.ManualDisabled, manualState(t, inner, site).PausedReason, "a failed enable must not release control")
	f.err = nil
	_, err = direct.UpdateChannelStatus(context.Background(), site, 7, 2, "operator", now)
	require.NoError(t, err, "retry disable remains available")
	_, err = direct.UpdateChannelStatus(context.Background(), site, 7, 1, "operator", now)
	require.NoError(t, err)
	require.Empty(t, manualState(t, inner, site).PausedReason)
}

func TestManualChannelExternalEnableReleasesHoldAndKeepsModeAndWeight(t *testing.T) {
	for _, mode := range []string{"auto", "observe", "off"} {
		t.Run(mode, func(t *testing.T) {
			_, inner, direct, f, site, now := manualFixture(t)
			policy := tuning.DefaultPolicy()
			policy.DispatchModes = map[string]string{"m": mode}
			require.NoError(t, inner.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: policy, Mode: "observe", UpdatedAt: now, UpdatedBy: "test"}))
			closed, err := direct.UpdateChannelStatus(context.Background(), site, 7, 2, "operator", now)
			require.NoError(t, err)
			held := manualState(t, inner, site)
			snapshot := func(status int, weight uint, at time.Time) {
				t.Helper()
				require.NoError(t, inner.StoreFreshChannels(site, []channelcontrol.Channel{{ID: 7, Name: "test", Models: "m", Weight: weight, Status: status}}, at))
			}
			// A collection started before CT's successful close cannot release it.
			snapshot(1, 100, closed.UpdatedAt.Add(-time.Millisecond))
			require.Equal(t, tuning.ManualDisabled, manualState(t, inner, site).PausedReason)
			snapshot(2, 0, closed.UpdatedAt.Add(time.Second))
			require.Equal(t, tuning.ManualDisabled, manualState(t, inner, site).PausedReason)
			// Model the next snapshot after the operator enables in NewAPI.
			snapshot(1, 0, closed.UpdatedAt.Add(2*time.Second))
			active := manualState(t, inner, site)
			require.Empty(t, active.PausedReason)
			require.False(t, active.CircuitDisabled)
			require.Equal(t, "normal", active.Phase)
			require.Greater(t, active.ControlRevision, held.ControlRevision)
			require.Zero(t, active.ProposedWeight, "enabling does not create recovery weight")
			require.False(t, active.SoftStartPending)
			require.Nil(t, active.ProbeCommandID)
			bases, err := inner.ListChannelBaseValues(site, "")
			require.NoError(t, err)
			require.Len(t, bases, 1)
			require.Zero(t, bases[0].CurrentWeight)
			actualPolicy, found, err := inner.GetPolicy(site)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, mode, actualPolicy.Policy.DispatchModes["m"])
			require.Len(t, f.updates, 1, "snapshot reconciliation must not write back to NewAPI")
			require.ErrorIs(t, inner.PutContinuousState(held), tuning.ErrDecisionSuperseded)
			require.NoError(t, inner.CheckManualChannelControl(tuning.Recommendation{InstanceID: site, ChannelID: 7, Evidence: map[string]any{"control_revision": active.ControlRevision}}))
			snapshot(1, 37, closed.UpdatedAt.Add(3*time.Second))
			require.Equal(t, active.ControlRevision, manualState(t, inner, site).ControlRevision, "repeated enabled snapshots must not reset tuning")
		})
	}
}

func TestManualChannelExternalEnableCannotReleaseInflightOrFailedClose(t *testing.T) {
	_, inner, direct, _, site, now := manualFixture(t)
	snapshot := func(status int, at time.Time) {
		t.Helper()
		require.NoError(t, inner.StoreFreshChannels(site, []channelcontrol.Channel{{ID: 7, Name: "test", Models: "m", Weight: 100, Status: status}}, at))
	}
	// The close is still in flight and the upstream remains enabled.
	cmd, err := inner.BeginManualChannelStatus(context.Background(), site, 7, 2, "operator", now)
	require.NoError(t, err)
	snapshot(1, now.Add(time.Second))
	require.Equal(t, tuning.ManualDisabled, manualState(t, inner, site).PausedReason)
	require.NoError(t, inner.FinishManualChannelStatus(site, cmd, false, now.Add(2*time.Second)))
	snapshot(1, now.Add(3*time.Second))
	require.Equal(t, tuning.ManualDisabled, manualState(t, inner, site).PausedReason, "unchanged enabled status is not proof of a new operator enable")
	// A subsequent confirmed disabled -> enabled transition resolves that hold.
	snapshot(2, now.Add(4*time.Second))
	snapshot(1, now.Add(5*time.Second))
	require.Empty(t, manualState(t, inner, site).PausedReason)
	// Even a disabled-to-enabled transition must wait for an active CT write.
	_, err = direct.UpdateChannelStatus(context.Background(), site, 7, 2, "operator", now.Add(6*time.Second))
	require.NoError(t, err)
	snapshot(2, now.Add(7*time.Second))
	cmd, err = inner.BeginManualChannelStatus(context.Background(), site, 7, 2, "operator", now.Add(8*time.Second))
	require.NoError(t, err)
	snapshot(1, now.Add(9*time.Second))
	require.Equal(t, tuning.ManualDisabled, manualState(t, inner, site).PausedReason)
	require.NoError(t, inner.FinishManualChannelStatus(site, cmd, true, now.Add(10*time.Second)))
	require.Equal(t, tuning.ManualDisabled, manualState(t, inner, site).PausedReason)
	snapshot(1, now.Add(11*time.Second))
	require.Empty(t, manualState(t, inner, site).PausedReason)
}

func TestManualChannelExternalEnableAfterDirectoryRebuild(t *testing.T) {
	db, inner, direct, _, site, now := manualFixture(t)
	closed, err := direct.UpdateChannelStatus(context.Background(), site, 7, 2, "operator", now)
	require.NoError(t, err)
	require.NotNil(t, manualState(t, inner, site).LastWriteAt)
	// A source switch may rebuild the current inventory without its old status.
	_, err = db.Exec(`DELETE FROM channel_current WHERE instance_id=?`, site)
	require.NoError(t, err)
	require.NoError(t, inner.StoreFreshChannels(site, []channelcontrol.Channel{{ID: 7, Name: "test", Models: "m", Weight: 43, Status: 1}}, closed.UpdatedAt.Add(time.Second)))
	active := manualState(t, inner, site)
	require.Empty(t, active.PausedReason)
	require.Equal(t, int64(43), active.ProposedWeight)
}

func TestManualChannelExpiresPendingAutomaticCommandsAndWaitsForDeliveredWrites(t *testing.T) {
	db, inner, direct, f, site, now := manualFixture(t)
	old := storage.ChannelCommand{ID: site + "-delivered", InstanceID: site, ChannelID: 7, CommandType: "channel.update", PayloadJSON: `{"weight":10}`, Status: "delivered", CreatedBy: "system:auto", CreatedAt: now, UpdatedAt: now}
	require.NoError(t, inner.CreateChannelCommand(old))
	_, err := direct.UpdateChannelStatus(context.Background(), site, 7, 2, "operator", now)
	require.ErrorIs(t, err, tuning.ErrChannelControlBusy)
	require.Empty(t, f.updates)
	_, _, err = inner.CompleteChannelCommand(old.ID, "failed", "canceled", now)
	require.NoError(t, err)
	probe, err := inner.CreateContinuousProbe(tuning.Recommendation{ID: site + "-probe", InstanceID: site, ChannelID: 7, Rule: "probe_started", ModeAtCreation: "auto", CreatedAt: now}, "m", 10, 0, now)
	require.NoError(t, err)
	_, err = direct.UpdateChannelStatus(context.Background(), site, 7, 2, "operator", now)
	require.NoError(t, err)
	status, err := inner.ContinuousCommandStatus(probe)
	require.NoError(t, err)
	require.Equal(t, "expired", status)
	var recommendationStatus string
	require.NoError(t, db.QueryRow(`SELECT status FROM tuning_recommendations WHERE command_id=?`, probe).Scan(&recommendationStatus))
	require.Equal(t, "expired", recommendationStatus)
	commands, err := inner.ClaimPendingCommands(site, now)
	require.NoError(t, err)
	require.Empty(t, commands)
}

type blockedStatusController struct {
	fakeController
	entered, release chan struct{}
	once             sync.Once
}

func (f *blockedStatusController) Update(ctx context.Context, r channelcontrol.UpdateRequest) (channelcontrol.Result, error) {
	if r.Status != nil && *r.Status == 1 {
		f.once.Do(func() { close(f.entered) })
		select {
		case <-f.release:
		case <-ctx.Done():
			return channelcontrol.Result{}, ctx.Err()
		}
	}
	return f.fakeController.Update(ctx, r)
}

func TestManualChannelCloseSerializesAfterInflightAutomaticEnable(t *testing.T) {
	_, inner, direct, _, site, now := manualFixture(t)
	f := &blockedStatusController{entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(f.release) }) }
	defer release()
	direct = direct.WithFactory(func(string, string, int64) Controller { return f }, nil)
	s := tuning.ContinuousState{InstanceID: site, ChannelID: 7, ModelName: "m", Phase: "probing", CircuitDisabled: true, CircuitStatusTarget: 1, ProposedWeight: 20, UpdatedAt: now}
	require.NoError(t, inner.PutContinuousState(s))
	disabled := 2
	require.NoError(t, inner.ApplyChannelWrite(site, 7, nil, nil, &disabled, now))
	enabled := 1
	rec := tuning.Recommendation{ID: site + "-recovery", InstanceID: site, ChannelID: 7, Rule: "circuit_recovered", ModeAtCreation: "auto", ProposedWeight: 20, ProposedChannelStatus: &enabled, CreatedAt: now}
	autoDone := make(chan error, 1)
	go func() { _, err := direct.CreateContinuousWeightChange(rec, "system:auto", now); autoDone <- err }()
	select {
	case <-f.entered:
	case err := <-autoDone:
		t.Fatalf("automatic enable never started: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("enable did not start")
	}
	manualDone := make(chan error, 1)
	go func() {
		_, err := direct.UpdateChannelStatus(context.Background(), site, 7, 2, "operator", now)
		manualDone <- err
	}()
	select {
	case err := <-manualDone:
		t.Fatalf("manual write passed inflight recovery: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	require.NoError(t, <-autoDone)
	require.NoError(t, <-manualDone)
	require.Len(t, f.updates, 2)
	require.Equal(t, 1, *f.updates[0].Status)
	require.Equal(t, 2, *f.updates[1].Status)
	require.Equal(t, tuning.ManualDisabled, manualState(t, inner, site).PausedReason)
	_, err := direct.CreateContinuousWeightChange(rec, "system:auto", time.Now())
	require.ErrorIs(t, err, tuning.ErrDecisionSuperseded)
}

func TestManualRecoveryProberDoesNotStartNextRequestAfterTakeover(t *testing.T) {
	active := true
	f := &fakeController{results: []channelcontrol.ProbeResult{{Success: true}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := ownedRecoveryProber{Controller: f, active: func() bool { return active }, cancel: cancel}
	_, err := p.Probe(ctx, 7, "m")
	require.NoError(t, err)
	active = false
	_, err = p.Probe(ctx, 7, "m")
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, f.probes, 1)
}
