package mysqlstore

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
	"github.com/stretchr/testify/require"
)

func TestSiteHistoryDirectWithoutAgentAndStableAgentOwnership(t *testing.T) {
	s, site, now := lifelineStore(t)
	// A legacy instance sharing the site's ID must not remap a direct audit.
	require.NoError(t, s.CreateInstance(storage.Instance{ID: site, SiteID: site + "-other", Name: "legacy", Enabled: true, CreatedAt: now, UpdatedAt: now}))
	direct, err := s.RecordDirectChannelGroupChange(site, 7, "admin", "a", "b", now.Add(-time.Minute))
	require.NoError(t, err)
	var auditSite string
	require.NoError(t, s.db.QueryRow("SELECT site_id FROM operation_audits WHERE id=?", direct.ID).Scan(&auditSite))
	require.Equal(t, site, auditSite)
	require.Empty(t, direct.InstanceID)
	require.Equal(t, "ct_direct", direct.ExecutionKind)
	preflight, err := s.RecordTuningPreflightResult(site, 7, "admin", "succeeded", "", now)
	require.NoError(t, err)
	read, found, err := s.GetTuningPreflight(site, preflight.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, site, read.SiteID)
	require.Equal(t, "ct_direct", read.ExecutionKind)
	node := site + "-node"
	require.NoError(t, s.CreateInstance(storage.Instance{ID: node, SiteID: site, Name: "test", Enabled: true, CreatedAt: now, UpdatedAt: now}))
	agent := storage.ChannelCommand{ID: rand.Text(), InstanceID: node, ChannelID: 7, CommandType: "channel.update", PayloadJSON: `{"weight":10}`, Status: "pending", CreatedAt: now, UpdatedAt: now}
	require.NoError(t, s.CreateChannelCommand(agent))
	t.Cleanup(func() { s.db.Exec("DELETE FROM channel_commands WHERE id=?", agent.ID) })
	claimed, err := s.ClaimPendingCommands(node, now)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, agent.ID, claimed[0].ID)
	// A different Agent cannot complete a queued command, or a CT command.
	_, changed, err := s.CompleteAgentChannelCommand("other", agent.ID, "succeeded", "", now)
	require.NoError(t, err)
	require.False(t, changed)
	_, err = s.db.Exec("UPDATE instances SET site_id=?,deleted=1 WHERE id=?", site+"-other", node)
	require.NoError(t, err)
	items, err := s.QueryChannelCommands(storage.ChannelCommandQuery{SiteID: site})
	require.NoError(t, err)
	require.Len(t, items, 3)
	other, err := s.QueryChannelCommands(storage.ChannelCommandQuery{SiteID: site + "-other"})
	require.NoError(t, err)
	require.Empty(t, other)
	page, err := s.QueryChannelLifeline(context.Background(), tuning.ProbeRecordQuery{Site: site, ChannelID: 7, From: now.Add(-time.Hour), To: now.Add(time.Second), Now: now, Limit: 20}, "changes")
	require.NoError(t, err)
	require.NotEmpty(t, page.Items)
	_, err = s.db.Exec("UPDATE channel_commands SET created_at=?,updated_at=? WHERE id=?", now.Add(-8*24*time.Hour), now.Add(-8*24*time.Hour), direct.ID)
	require.NoError(t, err)
	_, err = s.db.Exec("DELETE FROM operation_audits WHERE id=?", direct.ID)
	require.NoError(t, err)
	require.NoError(t, s.pruneChannelHistory(context.Background(), now))
	var count int
	require.NoError(t, s.db.QueryRow("SELECT COUNT(*) FROM channel_commands WHERE id=?", direct.ID).Scan(&count))
	require.Zero(t, count)
}

func TestLegacyChannelHistoryRequiresEvidence(t *testing.T) {
	s, site, now := lifelineStore(t)
	old := now.Add(-8 * 24 * time.Hour)
	node := site + "-legacy-node"
	require.NoError(t, s.CreateInstance(storage.Instance{ID: node, SiteID: site, Name: "test", Enabled: true, CreatedAt: now, UpdatedAt: now}))
	cases := []string{"direct", "agent", "unknown", "conflict", "site_only"}
	ids := map[string]string{}
	for _, kind := range cases {
		id := rand.Text()
		ids[kind] = id
		_, err := s.db.Exec(`INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at) VALUES(?,?,7,'channel.update','{}','succeeded','test',?,?)`, id, node, old, old)
		require.NoError(t, err)
		if kind != "unknown" {
			require.NoError(t, s.InsertRecommendation(tuning.Recommendation{ID: rand.Text(), InstanceID: site, ChannelID: 7, CreatedAt: old, Rule: "weight_write", CommandID: &id}))
		}
		if kind == "direct" || kind == "conflict" {
			evidenceSite := site
			if kind == "conflict" {
				evidenceSite = site + "-other"
			}
			require.NoError(t, s.BeginChannelControlRequest(context.Background(), tuning.ChannelControlRequest{ID: id, InstanceID: evidenceSite, ChannelID: 7, Operation: "channel.update", Payload: map[string]any{}, StartedAt: old}))
			require.NoError(t, s.LinkChannelControlRequest(context.Background(), id, id))
		}
		if kind == "agent" {
			_, err = s.db.Exec(`INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,before_summary,after_summary,status,created_at,updated_at) VALUES(?,?,'channel.update','channel','7','admin','{}',?,'succeeded',?,?)`, id, node, `{"result":{"applied_at":"2026-10-01T00:00:00Z"}}`, old, old)
			require.NoError(t, err)
		}
	}
	// Fresh database may include other fixtures. Drain the bounded backfill.
	for i := 0; i < 30; i++ {
		require.NoError(t, s.backfillChannelHistory(context.Background()))
	}
	for _, kind := range cases {
		var owner, execution, executor, legacy, proof string
		require.NoError(t, s.db.QueryRow("SELECT site_id,execution_kind,instance_id,legacy_instance_id,ownership_evidence FROM channel_commands WHERE id=?", ids[kind]).Scan(&owner, &execution, &executor, &legacy, &proof))
		require.Equal(t, node, legacy)
		switch kind {
		case "direct":
			require.Equal(t, site, owner)
			require.Equal(t, "ct_direct", execution)
			require.Empty(t, executor)
		case "agent":
			require.Equal(t, site, owner)
			require.Equal(t, "agent", execution)
			require.Equal(t, node, executor)
		case "site_only":
			require.Equal(t, site, owner)
			require.Equal(t, "unknown", execution)
			require.Empty(t, executor)
		default:
			require.Empty(t, owner)
			require.Empty(t, executor)
		}
	}
	require.NoError(t, s.pruneChannelHistory(context.Background(), now))
	for _, kind := range []string{"unknown", "conflict"} {
		var n int
		require.NoError(t, s.db.QueryRow("SELECT COUNT(*) FROM channel_commands WHERE id=?", ids[kind]).Scan(&n))
		require.Equal(t, 1, n)
	}
}

func TestCircuitDirectNotificationWithoutInstance(t *testing.T) {
	s, site, now := lifelineStore(t)
	rec := tuning.Recommendation{ID: rand.Text(), InstanceID: site, ChannelID: 7, ChannelName: "test", Rule: "circuit_opened", CreatedAt: now, ModeAtCreation: "auto", CurrentWeight: 100, ProposedWeight: 0}
	_, err := s.RecordDirectWeightChangeWithAudit(rec, "system:auto", now, storage.OperationAudit{})
	require.NoError(t, err)
	require.NoError(t, s.UpsertNotificationChannel(storage.NotificationChannel{ID: site, SiteID: site, ChannelType: "webhook", Enabled: true, CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, s.SyncCircuitAlerts(now))
	alerts, err := s.CircuitNotificationAlerts(now)
	require.NoError(t, err)
	found := false
	for _, a := range alerts {
		if a.SiteID == site {
			found = true
			require.Empty(t, a.InstanceID)
		}
	}
	require.True(t, found)
	visible, err := s.QueryAlerts(storage.AlertQuery{SiteID: site})
	require.NoError(t, err)
	require.Len(t, visible, 1)
	require.Equal(t, site, visible[0].SiteID)
}

func TestDirectProbeNeverClaimedOrCompletedByAgent(t *testing.T) {
	s, site, now := lifelineStore(t)
	id, err := s.CreateContinuousProbeExecuting(tuning.Recommendation{ID: rand.Text(), InstanceID: site, ChannelID: 7, CreatedAt: now, ModeAtCreation: "auto"}, "m", 1, 1, now)
	require.NoError(t, err)
	_, changed, err := s.CompleteAgentChannelCommand(site, id, "succeeded", "", now)
	require.NoError(t, err)
	require.False(t, changed)
	// Even a stale pending status cannot route CT execution through the Agent queue.
	_, err = s.db.Exec("UPDATE channel_commands SET status='pending' WHERE id=?", id)
	require.NoError(t, err)
	commands, err := s.ClaimPendingCommands("", now)
	require.NoError(t, err)
	require.Empty(t, commands)
	_, err = s.db.Exec("UPDATE channel_commands SET status='delivered' WHERE id=?", id)
	require.NoError(t, err)
	_, changed, err = s.CompleteChannelCommand(id, "succeeded", "", now)
	require.NoError(t, err)
	require.True(t, changed)
}
