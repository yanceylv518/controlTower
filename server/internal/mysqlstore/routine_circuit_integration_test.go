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

func TestRoutineCircuitLatchPersistsAndBlocksOrdinaryWrites(t *testing.T) {
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
	site := fmt.Sprintf("routine-circuit-%d", time.Now().UnixNano())
	_, err = db.Exec(`INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, site, site, site, now, now)
	require.NoError(t, err)
	defer func() {
		for _, table := range []string{"channel_current", "channel_base_values", "tuning_continuous_states"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site)
		}
		_, _ = db.Exec("DELETE FROM instances WHERE id=?", site)
	}()
	require.NoError(t, s.StoreInstanceChannels(site, []channelcontrol.Channel{{ID: 7, Name: "test", Models: "m", Weight: 100, Status: 1}}, now))
	state := tuning.ContinuousState{InstanceID: site, ChannelID: 7, ModelName: "m", Phase: "normal", UpdatedAt: now}
	require.NoError(t, s.PutContinuousState(state))
	states, err := New(db).ListContinuousStates(site)
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.False(t, states[0].RoutineCircuitLatched)
	state.RoutineCircuitLatched, state.CircuitDisabled = true, true
	state.LastWriteAt = &now
	require.NoError(t, s.PutContinuousState(state))
	states, err = New(db).ListContinuousStates(site)
	require.NoError(t, err)
	require.True(t, states[0].RoutineCircuitLatched)
	// The durable fault gate must reject stale ordinary decisions even when
	// their phase still says normal and before other evidence validation.
	rec := tuning.Recommendation{InstanceID: site, ChannelID: 7, Rule: "weight_write", ProposedWeight: 100, Evidence: map[string]any{"capacity_managed": true}}
	require.ErrorIs(t, checkContinuousCapacity(db, rec, now, ""), ErrCapacitySuperseded)
	// A newer externally enabled snapshot explicitly transfers ownership.
	require.NoError(t, s.StoreInstanceChannels(site, []channelcontrol.Channel{{ID: 7, Name: "test", Models: "m", Weight: 80, Status: 1}}, now.Add(time.Second)))
	states, err = New(db).ListContinuousStates(site)
	require.NoError(t, err)
	require.False(t, states[0].RoutineCircuitLatched)
	require.False(t, states[0].CircuitDisabled)
	require.Equal(t, int64(1), states[0].ControlRevision)
}
