package mysqlstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
	"github.com/stretchr/testify/require"
)

func TestChannelLifelineDuplicateHeavyPagination(t *testing.T) {
	s, site, now := lifelineStore(t)
	at := now.Add(-time.Minute)
	var expected []string
	// Mimic a day of direct-control operations: most commands must disappear
	// behind their request record before recommendation details are fetched.
	for batch := 0; batch < 7; batch++ {
		var commands, requests, recs []string
		var ca, ja, ra []any
		for i := 0; i < 200; i++ {
			id := fmt.Sprintf("%s-%04d", site[5:], batch*200+i)
			commands = append(commands, "(?,'ct_direct','recorded',?,'',7,'channel.update','{}','succeeded','test',?,?)")
			ca = append(ca, site, id, at, at)
			requests = append(requests, "(?,?,7,'channel.update','test','{}','succeeded','',0,?,?,?,?)")
			ja = append(ja, id, site, at, at, id, id)
			recs = append(recs, "(?,?,7,'test',?,'weight_write','{}',100,10,'auto','auto_executed',?)")
			ra = append(ra, id, site, at, id)
			expected = append(expected, "request:"+id)
		}
		_, err := s.db.Exec(`INSERT INTO channel_commands(site_id,execution_kind,ownership_evidence,id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at) VALUES `+strings.Join(commands, ","), ca...)
		require.NoError(t, err)
		_, err = s.db.Exec(`INSERT INTO channel_control_requests(id,instance_id,channel_id,operation,actor,payload_json,outcome,reason,duration_seconds,started_at,finished_at,command_id,recommendation_id) VALUES `+strings.Join(requests, ","), ja...)
		require.NoError(t, err)
		_, err = s.db.Exec(`INSERT INTO tuning_recommendations(id,instance_id,channel_id,channel_name,created_at,rule,evidence_json,current_weight,proposed_weight,mode_at_creation,status,command_id) VALUES `+strings.Join(recs, ","), ra...)
		require.NoError(t, err)
	}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("%s-alone-%d", site, i)
		_, err := s.db.Exec(`INSERT INTO channel_commands(site_id,execution_kind,ownership_evidence,id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at) VALUES(?,'ct_direct','recorded',?,'',7,'channel.update','{}','succeeded','test',?,?)`, site, id, at, at)
		require.NoError(t, err)
		expected = append(expected, "command:"+id)
		require.NoError(t, s.InsertRecommendation(tuning.Recommendation{ID: id, InstanceID: site, ChannelID: 7, ChannelName: "test", CreatedAt: at, Rule: "weight_write", Status: "observed", ModeAtCreation: "observe"}))
		expected = append(expected, "decision:"+id)
		require.NoError(t, s.InsertOperationAudit(storage.OperationAudit{ID: id + "-audit", SiteID: site, InstanceID: site, OperationType: "tuning.base_update", TargetType: "channel", TargetID: "7", Status: "succeeded", CreatedAt: at, UpdatedAt: at}))
		expected = append(expected, "audit:"+id+"-audit")
	}
	sort.Sort(sort.Reverse(sort.StringSlice(expected)))
	q := tuning.ProbeRecordQuery{Site: site, ChannelID: 7, Now: now, From: now.Add(-24 * time.Hour), To: now, Limit: 50}
	start := time.Now()
	page, err := s.QueryChannelLifeline(context.Background(), q, "changes")
	require.NoError(t, err)
	require.Len(t, page.Items, 50)
	t.Logf("1400 duplicate commands/requests/recommendations first page: %s", time.Since(start))
	sql, args, err := channelLifelineSQL(q, "changes", []any{site})
	require.NoError(t, err)
	var plan string
	require.NoError(t, s.db.QueryRow("EXPLAIN ANALYZE "+sql, args...).Scan(&plan))
	t.Log(plan)
	// Native source ordering must use the existing request index; the only
	// necessary global sorting is over the bounded union, not the day's requests.
	require.Contains(t, plan, "idx_control_channel_time")
	require.NotContains(t, plan, "Sort: j.started_at")
	q.Limit = 200
	var got []string
	for pages := 0; pages < 12; pages++ {
		page, err = s.QueryChannelLifeline(context.Background(), q, "changes")
		require.NoError(t, err)
		for _, event := range page.Items {
			got = append(got, event.ID)
		}
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
	}
	require.Equal(t, expected, got, "same-time cross-source pagination must not lose or duplicate events")
}
