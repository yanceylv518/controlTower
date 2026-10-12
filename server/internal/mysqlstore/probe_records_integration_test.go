package mysqlstore

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"testing"
	"time"

	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
	"github.com/stretchr/testify/require"
)

func lifelineStore(t *testing.T) (Store, string, time.Time) {
	t.Helper()
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated CT_MYSQL_TEST_DSN")
	}
	db, err := Open(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, ApplyDir(context.Background(), db, "../../migrations"))
	return New(db), "life-" + rand.Text(), time.Now().UTC().Truncate(time.Millisecond)
}

func TestProbeRecordsLifecycleMetricsPaginationAndRetention(t *testing.T) {
	s, site, now := lifelineStore(t)
	ctx := context.Background()
	base := tuning.ProbeRecord{ID: rand.Text(), InstanceID: site, ChannelID: 7, ChannelName: "seven", ModelName: "m", Kind: "scheduled", StartedAt: now.Add(-time.Minute), DeadlineAt: now.Add(-30 * time.Second), Outcome: "pending"}
	require.NoError(t, s.StoreProbeRecords(ctx, []tuning.ProbeRecord{base}, nil))
	final := base
	final.Outcome = "success"
	duration := 2.0
	final.DurationSeconds = &duration
	final.FinishedAt = &now
	require.NoError(t, s.StoreProbeRecords(ctx, []tuning.ProbeRecord{final, base}, nil)) // retry cannot regress terminal.
	for _, outcome := range []string{"slow", "error", "unknown", "canceled", "pending", "skipped"} {
		v := base
		v.ID = rand.Text()
		v.Outcome = outcome
		if outcome == "slow" || outcome == "error" {
			d := 30.0
			v.DurationSeconds = &d
		}
		if outcome == "skipped" {
			v.Reason = "global_concurrency"
		}
		require.NoError(t, s.StoreProbeRecords(ctx, []tuning.ProbeRecord{v}, nil))
	}
	other := final
	other.ID = rand.Text()
	other.InstanceID = site + "-other"
	require.NoError(t, s.StoreProbeRecords(ctx, []tuning.ProbeRecord{other}, nil))
	q := tuning.ProbeRecordQuery{Site: site, Now: now, From: now.Add(-time.Hour), To: now, Limit: 2}
	overview, err := s.QueryProbeOverview(ctx, q)
	require.NoError(t, err)
	require.Equal(t, 7, overview.RetentionDays)
	require.EqualValues(t, 7, overview.Total.Due)
	require.EqualValues(t, 6, overview.Total.Sent)
	require.EqualValues(t, 1, overview.Total.Success)
	require.EqualValues(t, 1, overview.Total.Slow)
	require.EqualValues(t, 1, overview.Total.Errors)
	require.EqualValues(t, 1, overview.Total.Unconfirmed)
	require.EqualValues(t, 0, overview.Total.Pending)
	require.EqualValues(t, 1, overview.Total.GlobalLimited)
	require.InDelta(t, 1.0/3, *overview.Total.SuccessRate, .0001)
	require.Equal(t, 2.0, *overview.Total.MeanSuccessSeconds)
	seen := map[string]bool{}
	for {
		page, e := s.QueryProbeRecords(ctx, q)
		require.NoError(t, e)
		require.EqualValues(t, 7, page.Total)
		for _, r := range page.Items {
			require.False(t, seen[r.ID])
			seen[r.ID] = true
			if r.ID == base.ID {
				require.Equal(t, "success", r.Outcome)
			}
		}
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
	}
	require.Len(t, seen, 7)
	q.Cursor = ""
	q.ChannelID = 999
	empty, err := s.QueryProbeOverview(ctx, q)
	require.NoError(t, err)
	require.Nil(t, empty.Total.SuccessRate)
	q.ChannelID = 7
	q.Outcome = "skipped"
	page, err := s.QueryProbeRecords(ctx, q)
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	old := final
	old.ID = rand.Text()
	old.StartedAt = now.Add(-8 * 24 * time.Hour)
	middle := old
	middle.ID = rand.Text()
	middle.StartedAt = now.Add(-2 * 24 * time.Hour)
	require.NoError(t, s.StoreProbeRecords(ctx, []tuning.ProbeRecord{old, middle}, nil))
	q.Outcome = ""
	q.From = now.Add(-30 * 24 * time.Hour)
	overview, err = s.QueryProbeOverview(ctx, q)
	require.NoError(t, err)
	require.EqualValues(t, 8, overview.Total.Due) // old hidden before cleanup.
	p := tuning.DefaultPolicy()
	p.Continuous.ChannelHistoryRetentionDays = 1
	require.NoError(t, s.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: p, Mode: "auto", UpdatedAt: now, UpdatedBy: "test"}))
	overview, err = s.QueryProbeOverview(ctx, q)
	require.NoError(t, err)
	require.Equal(t, 1, overview.RetentionDays)
	require.EqualValues(t, 7, overview.Total.Due)
	journal := tuning.ChannelControlRequest{ID: rand.Text(), InstanceID: site, ChannelID: 7, Operation: "channel.update", Payload: map[string]any{}, StartedAt: middle.StartedAt}
	require.NoError(t, s.BeginChannelControlRequest(ctx, journal))
	audit := storage.OperationAudit{ID: rand.Text(), InstanceID: site, OperationType: "channel.update", TargetType: "channel", TargetID: "7", ActorID: "test", Status: "succeeded", CreatedAt: old.StartedAt, UpdatedAt: old.StartedAt}
	require.NoError(t, s.InsertOperationAudit(audit))
	require.NoError(t, s.PruneProbeRecords(ctx, now))
	var count int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM tuning_probe_records WHERE instance_id=?`, site).Scan(&count))
	require.Equal(t, 7, count)
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM channel_control_requests WHERE id=?`, journal.ID).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM operation_audits WHERE id=?`, audit.ID).Scan(&count))
	require.Equal(t, 1, count)
}

func TestChannelLifelineMergedOperationsSafeFieldsAndRetention(t *testing.T) {
	s, site, now := lifelineStore(t)
	ctx := context.Background()
	at := now.Add(-time.Minute)
	command, recID := rand.Text(), rand.Text()
	insertCommand := func(id string) {
		_, err := s.db.Exec(`INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at) VALUES(?,?,7,'channel.update',?,'succeeded','test',?,?)`, id, site, `{"weight":0,"status":2,"secret":"never-show"}`, at, at)
		require.NoError(t, err)
	}
	insertCommand(command)
	decision := tuning.Recommendation{ID: recID, InstanceID: site, ChannelID: 7, ChannelName: "seven", CreatedAt: at, Rule: "weight_write", CurrentWeight: 100, ProposedWeight: 0, ModeAtCreation: "auto", Status: "auto_executed", CommandID: &command}
	require.NoError(t, s.InsertRecommendation(decision))
	request := tuning.ChannelControlRequest{ID: rand.Text(), InstanceID: site, ChannelID: 7, Operation: "channel.update", Actor: "test", RecommendationID: recID, StartedAt: at, Payload: map[string]any{"target": map[string]any{"weight": 0, "status": 2}}}
	require.NoError(t, s.BeginChannelControlRequest(ctx, request))
	request.Outcome = "succeeded"
	request.FinishedAt = &now
	require.NoError(t, s.FinishChannelControlRequest(ctx, request))
	// Link by recommendation even when the optional command back-link is lost.
	audit := storage.OperationAudit{ID: command, InstanceID: site, OperationType: "channel.update", TargetType: "channel", TargetID: "7", ActorID: "test", Status: "succeeded", BeforeSummary: `{"secret":"never-show"}`, AfterSummary: `{"weight":0}`, CreatedAt: at, UpdatedAt: at}
	require.NoError(t, s.InsertOperationAudit(audit))
	decision.ID = rand.Text()
	decision.CommandID = nil
	decision.Status = "observed"
	require.NoError(t, s.InsertRecommendation(decision))
	// Duplicate decision references must not duplicate legacy command rows.
	legacy := rand.Text()
	insertCommand(legacy)
	for i := 0; i < 2; i++ {
		v := decision
		v.ID = rand.Text()
		v.CommandID = &legacy
		require.NoError(t, s.InsertRecommendation(v))
	}
	pending := request
	pending.ID = rand.Text()
	pending.RecommendationID = ""
	require.NoError(t, s.BeginChannelControlRequest(ctx, pending))
	q := tuning.ProbeRecordQuery{Site: site, ChannelID: 7, Now: now, From: now.Add(-time.Hour), To: now, Limit: 50}
	page, err := s.QueryChannelLifeline(ctx, q, "all")
	require.NoError(t, err)
	require.Len(t, page.Items, 4)
	raw, err := json.Marshal(page)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "never-show")
	foundPending := false
	for _, v := range page.Items {
		if v.ID == "request:"+pending.ID {
			foundPending = true
			require.Equal(t, "unconfirmed", v.Outcome)
		}
	}
	require.True(t, foundPending)
	q.Limit = 1
	seen := map[string]bool{}
	for {
		p, e := s.QueryChannelLifeline(ctx, q, "all")
		require.NoError(t, e)
		for _, v := range p.Items {
			require.False(t, seen[v.ID])
			seen[v.ID] = true
		}
		if p.NextCursor == "" {
			break
		}
		q.Cursor = p.NextCursor
	}
	require.Len(t, seen, 4)
	q.Cursor = ""
	q.Site = site + "-other"
	page, err = s.QueryChannelLifeline(ctx, q, "all")
	require.NoError(t, err)
	require.Empty(t, page.Items)
	q.Site = site
	q.Now = now.Add(8 * 24 * time.Hour)
	page, err = s.QueryChannelLifeline(ctx, q, "all")
	require.NoError(t, err)
	require.Empty(t, page.Items)
}
