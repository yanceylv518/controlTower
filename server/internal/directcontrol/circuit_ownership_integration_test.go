package directcontrol

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/mysqlstore"
	"controltower/server/internal/secrets"
	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
	"github.com/stretchr/testify/require"
)

func TestDirectRecoveryStopsAfterExternalOwnershipChange(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated CT_MYSQL_TEST_DSN")
	}
	db, err := mysqlstore.Open(dsn)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, mysqlstore.ApplyDir(context.Background(), db, "../../migrations"))
	inner := mysqlstore.New(db)
	now := time.Now().UTC().Truncate(time.Second)
	site := fmt.Sprintf("direct-origin-%d", time.Now().UnixNano())
	require.NoError(t, inner.CreateInstance(storage.Instance{ID: site, SiteID: site, Name: site, Enabled: true, CreatedAt: now, UpdatedAt: now}))
	defer func() {
		for _, table := range []string{"channel_commands", "tuning_recommendations", "operation_audits", "channel_current", "channel_base_values", "tuning_continuous_states", "tuning_policies"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site)
		}
		_, _ = db.Exec("DELETE FROM instances WHERE id=?", site)
	}()
	encrypted, err := secrets.Encrypt("test-key", "test-token")
	require.NoError(t, err)
	require.NoError(t, inner.UpdateControlConfigForSite(site, "http://test.invalid", encrypted, 7, now))
	f := &fakeController{}
	direct := Wrap(inner, "test-key").WithFactory(func(string, string, int64) Controller { return f }, nil)
	snapshot := func(status int, at time.Time) {
		require.NoError(t, inner.StoreFreshChannels(site, []channelcontrol.Channel{{ID: 7, Name: "test", Models: "m", Weight: 100, Status: status}}, at))
	}
	snapshot(1, now)
	state := tuning.ContinuousState{InstanceID: site, ChannelID: 7, ModelName: "m", CircuitDisabled: true, Phase: "circuit", UpdatedAt: now, LastWriteAt: &now}
	require.NoError(t, inner.PutContinuousState(state))
	snapshot(2, now.Add(time.Second))
	snapshot(1, now.Add(2*time.Second))
	snapshot(2, now.Add(3*time.Second))
	// The in-flight evaluation began before the external enable/disable cycle.
	state.CircuitStatusTarget = 1
	state.ProposedWeight = 20
	require.ErrorIs(t, inner.PutContinuousState(state), tuning.ErrDecisionSuperseded)
	enabled := 1
	_, err = direct.CreateContinuousWeightChange(tuning.Recommendation{InstanceID: site, ChannelID: 7, Rule: "circuit_recovered", ModeAtCreation: "auto", ProposedWeight: 20, ProposedChannelStatus: &enabled, Evidence: map[string]any{"control_revision": int64(0)}}, "system:auto", now.Add(4*time.Second))
	require.ErrorIs(t, err, tuning.ErrDecisionSuperseded)
	require.Empty(t, f.updates, "stale recovery must stop before any NewAPI write")
}
