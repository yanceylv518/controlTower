package mysqlstore

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
	"github.com/stretchr/testify/require"
)

func TestChannelHistoryRetentionOwnershipAndActiveProtection(t *testing.T) {
	s, site, now := lifelineStore(t)
	ctx := context.Background()
	agent := site + "-agent"
	_, err := s.db.Exec(`INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,'test','test','','',0,?,?)`, agent, site, now, now)
	require.NoError(t, err)
	p := tuning.DefaultPolicy()
	p.Continuous.ChannelHistoryRetentionDays = 2
	require.NoError(t, s.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: p, Mode: "auto", UpdatedAt: now, UpdatedBy: "test"}))
	old := now.Add(-3 * 24 * time.Hour)
	for _, tc := range []struct {
		name, owner, status string
		created, updated    time.Time
		keep, reference     bool
	}{
		{"site terminal", site, "succeeded", old, old, false, false},
		{"agent terminal", agent, "failed", old, old, false, false},
		{"expired", agent, "expired", old, old, false, false},
		{"pending", agent, "pending", old, old, true, false},
		{"delivered", agent, "delivered", old, old, true, false},
		{"executing", agent, "executing", old, old, true, false},
		{"unknown", agent, "unknown", old, old, true, false},
		{"late completion", agent, "succeeded", old, now, true, false},
		{"boundary", agent, "succeeded", now.Add(-48 * time.Hour), old, true, false},
		{"other site default 7d", site + "-other", "succeeded", old, old, true, false},
		{"referenced terminal", agent, "succeeded", old, old, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := rand.Text()
			_, err := s.db.Exec(`INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at) VALUES(?,?,7,'channel.update','{}',?,'test',?,?)`, id, tc.owner, tc.status, tc.created, tc.updated)
			require.NoError(t, err)
			if tc.reference {
				_, err = s.db.Exec(`INSERT INTO tuning_continuous_states(instance_id,channel_id,model_name,probe_command_id,updated_at) VALUES(?,7,'m',?,?)`, site, id, now)
				require.NoError(t, err)
			}
			require.NoError(t, s.pruneChannelHistory(ctx, now))
			var count int
			require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM channel_commands WHERE id=?`, id).Scan(&count))
			require.Equal(t, tc.keep, count == 1)
		})
	}
	for _, tc := range []struct {
		name, target, status, owner string
		keep                        bool
	}{
		{"channel old", "channel", "succeeded", agent, false},
		{"channel pending", "channel", "submitted", agent, true},
		{"non channel", "configuration", "succeeded", agent, true},
		{"other site", "channel", "succeeded", site + "-other", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := rand.Text()
			require.NoError(t, s.InsertOperationAudit(storage.OperationAudit{ID: id, InstanceID: tc.owner, TargetType: tc.target, TargetID: "7", OperationType: "channel.update", Status: tc.status, CreatedAt: old, UpdatedAt: old}))
			require.NoError(t, s.pruneChannelHistory(ctx, now))
			var count int
			require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM operation_audits WHERE id=?`, id).Scan(&count))
			require.Equal(t, tc.keep, count == 1)
		})
	}
}

func TestChannelHistoryRetentionFailureDoesNotBlockOtherOwners(t *testing.T) {
	s, site, now := lifelineStore(t)
	old := now.Add(-8 * 24 * time.Hour)
	bad, good := site+"-a", site+"-b"
	for _, owner := range []string{bad, good} {
		_, err := s.db.Exec(`INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at) VALUES(?,?,7,'channel.update','{}','succeeded','test',?,?)`, owner, owner, old, old)
		require.NoError(t, err)
	}
	// Invalid stored JSON deterministically fails a protection check. It must
	// neither relax the guard nor prevent the healthy owner's cleanup.
	_, err := s.db.Exec(`INSERT INTO tuning_continuous_states(instance_id,channel_id,model_name,capacity_control_json,updated_at) VALUES(?,7,'m','{',?)`, bad, now)
	require.NoError(t, err)
	t.Cleanup(func() {
		s.db.Exec(`DELETE FROM tuning_continuous_states WHERE instance_id=?`, bad)
		s.db.Exec(`DELETE FROM channel_commands WHERE id IN (?,?)`, bad, good)
	})
	err = s.pruneChannelHistory(context.Background(), now)
	require.ErrorContains(t, err, "protections")
	var n int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM channel_commands WHERE id=?`, bad).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM channel_commands WHERE id=?`, good).Scan(&n))
	require.Zero(t, n)
}

func TestChannelHistoryRetentionLinkedAuditAndBatch(t *testing.T) {
	s, site, now := lifelineStore(t)
	ctx := context.Background()
	old := now.Add(-8 * 24 * time.Hour)
	command := rand.Text()
	_, err := s.db.Exec(`INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at) VALUES(?,?,7,'channel.update','{}','delivered','test',?,?)`, command, site, old, old)
	require.NoError(t, err)
	audit := storage.OperationAudit{ID: rand.Text(), InstanceID: site, TargetType: "channel", TargetID: "7", OperationType: "channel.update", CorrelationID: command, Status: "succeeded", CreatedAt: old, UpdatedAt: old}
	require.NoError(t, s.InsertOperationAudit(audit))
	require.NoError(t, s.pruneChannelHistory(ctx, now))
	var n int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM operation_audits WHERE id=?`, audit.ID).Scan(&n))
	require.Equal(t, 1, n)
	_, err = s.db.Exec(`UPDATE channel_commands SET status='succeeded',updated_at=? WHERE id=?`, old, command)
	require.NoError(t, err)
	require.NoError(t, s.pruneChannelHistory(ctx, now)) // command removed first
	require.NoError(t, s.pruneChannelHistory(ctx, now))
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM operation_audits WHERE id=?`, audit.ID).Scan(&n))
	require.Zero(t, n)
	values := []string{}
	args := []any{}
	for i := 0; i < channelHistoryBatchSize+5; i++ {
		values = append(values, "(?,?,7,'channel.update','{}','succeeded','test',?,?)")
		args = append(args, rand.Text(), site, old, old)
	}
	_, err = s.db.Exec(`INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at) VALUES `+strings.Join(values, ","), args...)
	require.NoError(t, err)
	require.NoError(t, s.pruneChannelHistory(ctx, now))
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM channel_commands WHERE instance_id=?`, site).Scan(&n))
	require.Equal(t, 5, n)
	require.NoError(t, s.pruneChannelHistory(ctx, now))
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM channel_commands WHERE instance_id=?`, site).Scan(&n))
	require.Zero(t, n)
}

func TestChannelHistoryRetentionAdvancesPastProtectedCandidates(t *testing.T) {
	s, site, now := lifelineStore(t)
	old := now.Add(-8 * 24 * time.Hour)
	command := rand.Text()
	_, err := s.db.Exec(`INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at) VALUES(?,?,7,'channel.update','{}','delivered','test',?,?)`, command, site, old, old)
	require.NoError(t, err)
	values := []string{}
	args := []any{}
	for i := 0; i < channelHistoryBatchSize; i++ {
		values = append(values, "(?,?,'channel.update','channel','7','test','{}','{}','succeeded',?,?,?)")
		args = append(args, rand.Text(), site, command, old, old)
	}
	_, err = s.db.Exec(`INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,before_summary,after_summary,status,correlation_id,created_at,updated_at) VALUES `+strings.Join(values, ","), args...)
	require.NoError(t, err)
	eligible := rand.Text()
	require.NoError(t, s.InsertOperationAudit(storage.OperationAudit{ID: eligible, InstanceID: site, TargetType: "channel", TargetID: "7", OperationType: "channel.update", Status: "succeeded", CreatedAt: old.Add(time.Second), UpdatedAt: old}))
	for pass := 0; pass < 2; pass++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = s.pruneChannelHistory(ctx, now)
		cancel()
		require.NoError(t, err)
	}
	var n int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM operation_audits WHERE id=?`, eligible).Scan(&n))
	require.Zero(t, n)
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM operation_audits WHERE instance_id=?`, site).Scan(&n))
	require.Equal(t, channelHistoryBatchSize, n)
	// Once the protective command ends, wrapping the scan revisits old rows.
	_, err = s.db.Exec(`UPDATE channel_commands SET status='succeeded' WHERE id=?`, command)
	require.NoError(t, err)
	require.NoError(t, s.pruneChannelHistory(context.Background(), now))
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM operation_audits WHERE instance_id=?`, site).Scan(&n))
	require.Zero(t, n)
}

func TestChannelLifelineLargeHistory(t *testing.T) {
	if os.Getenv("CT_LIFELINE_SCALE") != "1" {
		t.Skip("explicit isolated scale test")
	}
	s, site, now := lifelineStore(t)
	digit := `(SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9)`
	at := now.Add(-time.Hour)
	for batch := 0; batch < 50; batch++ {
		sequence := fmt.Sprintf("(SELECT a.n+10*b.n+100*c.n+1000*d.n+%d n FROM ", batch*10000) + digit + " a CROSS JOIN " + digit + " b CROSS JOIN " + digit + " c CROSS JOIN " + digit + " d) seq"
		_, err := s.db.Exec(`INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at) SELECT CONCAT(?,n),?,1+MOD(n,1000),'channel.update','{}','succeeded','test',?,? FROM `+sequence, site, site, at, at)
		require.NoError(t, err)
		_, err = s.db.Exec(`INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,before_summary,after_summary,status,created_at,updated_at) SELECT CONCAT('a',?,n),?,'tuning.base_update','channel',CAST(1+MOD(n,1000) AS CHAR),'test','{}','{}','succeeded',?,? FROM `+sequence, site, site, at, at)
		require.NoError(t, err)
	}
	q := tuning.ProbeRecordQuery{Site: site, ChannelID: 7, Now: now, From: now.Add(-24 * time.Hour), To: now, Limit: 50}
	query, args, err := channelLifelineSQL(q, "changes", []any{site})
	require.NoError(t, err)
	plan, err := s.db.Query("EXPLAIN "+query, args...)
	require.NoError(t, err)
	cols, err := plan.Columns()
	require.NoError(t, err)
	for plan.Next() {
		cells := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range cells {
			dest[i] = &cells[i]
		}
		require.NoError(t, plan.Scan(dest...))
		values := map[string]string{}
		for i, col := range cols {
			if v, ok := cells[i].([]byte); ok {
				values[col] = string(v)
			} else {
				values[col] = fmt.Sprint(cells[i])
			}
		}
		if values["table"] == "c" || values["table"] == "a" {
			require.NotEqual(t, "ALL", values["type"], values)
			t.Logf("plan %s: type=%s key=%s rows=%s", values["table"], values["type"], values["key"], values["rows"])
		}
	}
	require.NoError(t, plan.Err())
	plan.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	page, err := s.QueryChannelLifeline(ctx, q, "changes")
	require.NoError(t, err)
	require.Len(t, page.Items, 50)
	require.NotEmpty(t, page.NextCursor)
	t.Logf("500000 commands + 500000 audits, page: %s", time.Since(start))
	q.Cursor = page.NextCursor
	start = time.Now()
	next, err := s.QueryChannelLifeline(ctx, q, "changes")
	require.NoError(t, err)
	require.Len(t, next.Items, 50)
	t.Logf("second page: %s", time.Since(start))
	pruneCtx, pruneCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pruneCancel()
	start = time.Now()
	require.NoError(t, s.pruneChannelHistory(pruneCtx, now.Add(8*24*time.Hour)))
	t.Logf("bounded cleanup: %s", time.Since(start))
}

func TestChannelHistoryLargeCleanup(t *testing.T) {
	if os.Getenv("CT_LIFELINE_SCALE") != "1" {
		t.Skip("explicit isolated scale test")
	}
	s, _, now := lifelineStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	require.NoError(t, s.pruneChannelHistory(ctx, now.Add(8*24*time.Hour)))
	t.Logf("bounded cleanup over existing isolated history: %s", time.Since(start))
}

func TestChannelHistoryRetentionRecentReferences(t *testing.T) {
	s, site, now := lifelineStore(t)
	old := now.Add(-8 * 24 * time.Hour)
	for _, kind := range []string{"pending_probe", "recent_audit", "recent_recommendation"} {
		t.Run(kind, func(t *testing.T) {
			id := rand.Text()
			_, err := s.db.Exec(`INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at) VALUES(?,?,7,'channel.update','{}','succeeded','test',?,?)`, id, site, old, old)
			require.NoError(t, err)
			switch kind {
			case "pending_probe":
				require.NoError(t, s.StoreProbeRecords(context.Background(), []tuning.ProbeRecord{{ID: rand.Text(), InstanceID: site, ChannelID: 7, ModelName: "m", Kind: "recovery", StartedAt: now, DeadlineAt: now.Add(time.Minute), Outcome: "pending", RoundID: id}}, nil))
			case "recent_audit":
				require.NoError(t, s.InsertOperationAudit(storage.OperationAudit{ID: rand.Text(), InstanceID: site, TargetType: "channel", TargetID: "7", OperationType: "channel.update", CorrelationID: id, Status: "succeeded", CreatedAt: old, UpdatedAt: now}))
			case "recent_recommendation":
				require.NoError(t, s.InsertRecommendation(tuning.Recommendation{ID: rand.Text(), InstanceID: site, ChannelID: 7, ChannelName: "test", CreatedAt: now, Rule: "weight_write", ModeAtCreation: "auto", Status: "auto_executed", CommandID: &id}))
			}
			require.NoError(t, s.pruneChannelHistory(context.Background(), now))
			var count int
			require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM channel_commands WHERE id=?`, id).Scan(&count))
			require.Equal(t, 1, count)
		})
	}
}

func TestChannelLifelineAgentOwnershipAndBoundedPages(t *testing.T) {
	s, site, now := lifelineStore(t)
	ctx := context.Background()
	agent := site + "-agent"
	_, err := s.db.Exec(`INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,'test','test','','',1,?,?)`, agent, site, now, now)
	require.NoError(t, err)
	at := now.Add(-time.Minute)
	for i := 0; i < 12; i++ {
		id := rand.Text()
		_, err = s.db.Exec(`INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at) VALUES(?,?,7,'channel.update','{}','succeeded','test',?,?)`, id, agent, at, at)
		require.NoError(t, err)
		require.NoError(t, s.InsertOperationAudit(storage.OperationAudit{ID: rand.Text(), InstanceID: agent, TargetType: "channel", TargetID: "7", OperationType: "tuning.base_update", Status: "succeeded", CreatedAt: at, UpdatedAt: at}))
	}
	q := tuning.ProbeRecordQuery{Site: site, ChannelID: 7, Now: now, From: now.Add(-time.Hour), To: now, Limit: 200}
	full, err := s.QueryChannelLifeline(ctx, q, "changes")
	require.NoError(t, err)
	require.Len(t, full.Items, 24)
	q.Limit = 3
	var ids []string
	for {
		page, err := s.QueryChannelLifeline(ctx, q, "changes")
		require.NoError(t, err)
		for _, v := range page.Items {
			ids = append(ids, v.ID)
		}
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
		require.LessOrEqual(t, len(ids), 24)
	}
	var expected []string
	for _, v := range full.Items {
		expected = append(expected, v.ID)
	}
	require.Equal(t, expected, ids)
	q.Cursor = ""
	q.Site = site + "-other"
	page, err := s.QueryChannelLifeline(ctx, q, "changes")
	require.NoError(t, err)
	require.Empty(t, page.Items)
}
