package mysqlstore

import (
	"context"
	"controltower/server/internal/tuning"
	"crypto/rand"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestLifelineRuntimeAndRecoveryBatchIsolation(t *testing.T) {
	s, site, now := lifelineStore(t)
	ctx := context.Background()
	runtime := tuning.ProbeRuntime{InstanceID: site, ChannelID: 7, Model: "m", ConfirmedAt: now, Running: true, Pending: 1}
	require.NoError(t, s.StoreProbeRuntime(ctx, []tuning.ProbeRuntime{runtime}))
	old := runtime
	old.ConfirmedAt = now.Add(-time.Minute)
	old.Running = false
	require.NoError(t, s.StoreProbeRuntime(ctx, []tuning.ProbeRuntime{old}))
	values, err := s.QueryProbeRuntime(ctx, site)
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.True(t, values[0].Running)
	values, err = s.QueryProbeRuntime(ctx, site+"-other")
	require.NoError(t, err)
	require.Empty(t, values)
	id := rand.Text()
	_, err = s.db.Exec(`INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,error_summary,created_at,updated_at) VALUES(?,?,7,'channel.probe','{"model":"m","probe_count":10}','executing','system:auto','',?,?)`, id, site, now.Add(-time.Minute), now)
	require.NoError(t, err)
	records := []tuning.ProbeRecord{}
	for i, outcome := range []string{"success", "error", "pending"} {
		records = append(records, tuning.ProbeRecord{ID: rand.Text(), InstanceID: site, ChannelID: 7, ModelName: "m", Kind: "recovery", RoundID: id, RoundIndex: i + 1, StartedAt: now.Add(-30 * time.Second), DeadlineAt: now.Add(-time.Second), Outcome: outcome})
	}
	require.NoError(t, s.StoreProbeRecords(ctx, records, nil))
	q := tuning.ProbeRecordQuery{Site: site, ChannelID: 7, From: now.Add(-time.Hour), To: now, Now: now, Limit: 1}
	batch, err := s.QueryProbeRounds(ctx, q)
	require.NoError(t, err)
	require.Len(t, batch.Items, 1)
	require.Equal(t, 10, batch.Items[0].Planned)
	require.EqualValues(t, 3, batch.Items[0].Metrics.Sent)
	require.EqualValues(t, 1, batch.Items[0].Metrics.Unconfirmed)
	require.Zero(t, batch.Items[0].Metrics.Pending)
	q.RoundID = id
	page, err := s.QueryProbeRecords(ctx, q)
	require.NoError(t, err)
	require.EqualValues(t, 3, page.Total)
	require.Equal(t, id, page.Items[0].RoundID)
	require.Positive(t, page.Items[0].RoundIndex)
	q.Dispatch = "unsent"
	page, err = s.QueryProbeRecords(ctx, q)
	require.NoError(t, err)
	require.Zero(t, page.Total)
	q.Site = site + "-other"
	batch, err = s.QueryProbeRounds(ctx, q)
	require.NoError(t, err)
	require.Empty(t, batch.Items)
	q.Site = site
	q.ChannelID = 8
	batch, err = s.QueryProbeRounds(ctx, q)
	require.NoError(t, err)
	require.Empty(t, batch.Items)
	// The current page also obtains progress without querying each channel.
	require.NoError(t, s.PutContinuousState(tuning.ContinuousState{InstanceID: site, ChannelID: 7, ModelName: "m", Phase: "probing", ProbeCommandID: &id, UpdatedAt: now}))
	active, err := s.currentRecoveryRounds(ctx, site, now, 7)
	require.NoError(t, err)
	require.EqualValues(t, 3, active[7].Metrics.Sent)
	require.EqualValues(t, 1, active[7].Metrics.Unconfirmed)
	current, err := s.QueryLifelineCurrent(ctx, site)
	require.NoError(t, err)
	require.NotNil(t, current.Channels)
	// An actual command must remain visible even if the old phase/marker was
	// cleared (for example by manual close). Otherwise it looks falsely idle.
	_, err = s.db.Exec(`DELETE FROM tuning_continuous_states WHERE instance_id=?`, site)
	require.NoError(t, err)
	active, err = s.currentRecoveryRounds(ctx, site, now, 7)
	require.NoError(t, err)
	require.Equal(t, id, active[7].ID)
	_, err = s.db.Exec(`UPDATE channel_commands SET status='succeeded' WHERE id=?`, id)
	require.NoError(t, err)
	active, err = s.currentRecoveryRounds(ctx, site, now, 7)
	require.NoError(t, err)
	require.Empty(t, active)
	// A completed command with a still-in-flight request is not idle yet.
	_, err = s.db.Exec(`UPDATE tuning_probe_records SET deadline_at=? WHERE id=?`, now.Add(time.Minute), records[2].ID)
	require.NoError(t, err)
	active, err = s.currentRecoveryRounds(ctx, site, now, 7)
	require.NoError(t, err)
	require.EqualValues(t, 1, active[7].Metrics.Pending)
	_, err = s.db.Exec(`UPDATE tuning_probe_records SET channel_id=8 WHERE id=?`, records[2].ID)
	require.NoError(t, err)
	active, err = s.currentRecoveryRounds(ctx, site, now, 7)
	require.NoError(t, err)
	require.Empty(t, active, "a pending request from another channel cannot activate this round")
	_, err = s.db.Exec(`UPDATE tuning_probe_records SET channel_id=7,started_at=? WHERE id=?`, now.Add(-8*24*time.Hour), records[2].ID)
	require.NoError(t, err)
	active, err = s.currentRecoveryRounds(ctx, site, now, 7)
	require.NoError(t, err)
	require.Empty(t, active, "records outside retention cannot activate a completed round")
	other, err := s.currentRecoveryRounds(ctx, site+"-other", now, 7)
	require.NoError(t, err)
	require.Empty(t, other)
}
