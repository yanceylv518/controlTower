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

func TestProbeSpeedPersistsAggregatesAndRejectsOldOwnership(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated CT_MYSQL_TEST_DSN")
	}
	db, err := Open(dsn)
	require.NoError(t, err)
	defer db.Close()
	ctx := context.Background()
	require.NoError(t, ApplyDir(ctx, db, "../../migrations"))
	s := New(db)
	now := time.Now().UTC().Truncate(time.Minute)
	site := fmt.Sprintf("probe-speed-%d", time.Now().UnixNano())
	_, err = db.Exec(`INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, site, site, site, now, now)
	require.NoError(t, err)
	defer func() {
		for _, table := range []string{"tuning_routine_probe_minutes", "tuning_continuous_states", "channel_base_values", "channel_current"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site)
		}
		_, _ = db.Exec("DELETE FROM instances WHERE id=?", site)
	}()
	require.NoError(t, s.StoreInstanceChannels(site, []channelcontrol.Channel{{ID: 7, Models: "m", Weight: 100, Status: 1}}, now.Add(-time.Hour)))
	bases, err := s.ListChannelBaseValues(site, "")
	require.NoError(t, err)
	require.Len(t, bases, 1)
	v := tuning.RoutineProbeSample{InstanceID: site, ChannelID: 7, ModelName: "m", BaseUpdatedAt: bases[0].UpdatedAt, StartedAt: now.Add(-50 * time.Second), DurationSeconds: .25}
	require.NoError(t, s.RecordRoutineProbeSample(ctx, v))
	v.StartedAt = now.Add(-40 * time.Second)
	v.DurationSeconds = 1.25
	require.NoError(t, s.RecordRoutineProbeSample(ctx, v))
	v.StartedAt = now.Add(5 * time.Second)
	v.DurationSeconds = 20
	require.NoError(t, s.RecordRoutineProbeSample(ctx, v))
	stats, err := New(db).QueryRoutineProbeSpeeds(ctx, site, now.Add(-time.Minute), now)
	require.NoError(t, err)
	require.Equal(t, int64(2), stats[7].Samples)
	require.InDelta(t, .75, stats[7].MeanSeconds, 1e-10)
	state := tuning.ContinuousState{InstanceID: site, ChannelID: 7, ModelName: "m", Phase: "normal", UpdatedAt: now, ProbeSpeed: stats[7]}
	state.ProbeSpeed.Factor = 1.2
	state.ProbeSpeed.Ready = true
	require.NoError(t, s.PutContinuousState(state))
	states, err := New(db).ListContinuousStates(site)
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.Equal(t, state.ProbeSpeed, states[0].ProbeSpeed)
	_, err = db.Exec(`UPDATE tuning_continuous_states SET control_revision=1 WHERE instance_id=?`, site)
	require.NoError(t, err)
	stats, err = s.QueryRoutineProbeSpeeds(ctx, site, now.Add(-time.Minute), now)
	require.NoError(t, err)
	require.Empty(t, stats)
	v.ControlRevision = 1
	v.StartedAt = now.Add(-30 * time.Second)
	v.DurationSeconds = 30
	require.NoError(t, s.RecordRoutineProbeSample(ctx, v))
	stats, err = s.QueryRoutineProbeSpeeds(ctx, site, now.Add(-time.Minute), now)
	require.NoError(t, err)
	require.Equal(t, int64(1), stats[7].Samples)
	require.Equal(t, float64(30), stats[7].MeanSeconds)
	v.StartedAt = now.Add(-49 * time.Hour)
	require.NoError(t, s.RecordRoutineProbeSample(ctx, v))
	require.NoError(t, s.PruneRoutineProbeSamples(ctx, now.Add(-48*time.Hour)))
	var oldRows int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM tuning_routine_probe_minutes WHERE instance_id=? AND bucket_time<?`, site, now.Add(-48*time.Hour)).Scan(&oldRows))
	require.Zero(t, oldRows)
}
