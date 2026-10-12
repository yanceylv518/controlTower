package mysqlstore

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"controltower/server/internal/tuning"
	"github.com/stretchr/testify/require"
)

func TestProbeCoverageIdempotentSnapshotsWindowAndRestart(t *testing.T) {
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
	site := fmt.Sprintf("coverage-%d", time.Now().UnixNano())
	defer db.Exec(`DELETE FROM tuning_routine_probe_coverage WHERE instance_id IN (?,?)`, site, site+"-other")
	at := now.Add(10 * time.Second)
	v := tuning.ProbeCoverageBucket{InstanceID: site, ChannelID: 7, RunID: "run-a", BucketTime: now, Scheduled: true, ProbeDispatchCounts: tuning.ProbeDispatchCounts{Due: 1, Sent: 1, LastDueAt: at, LastSentAt: &at}}
	write := func(rows ...tuning.ProbeCoverageBucket) {
		t.Helper()
		require.NoError(t, s.StoreRoutineProbeCoverage(ctx, rows))
	}
	write(v)
	old := v
	v.Due, v.GlobalLimited = 2, 1
	v.LastSkippedAt = &at
	write(v)
	write(v)
	write(old) // retries and out-of-order old snapshots cannot double-count/regress.
	v.Scheduled = false
	v.Due, v.Sent, v.GlobalLimited, v.EvidenceLimited = 3, 2, 0, 1
	write(v)
	v = old
	v.RunID = "run-b"
	write(v) // a process restart contributes a distinct generation.
	v = old
	v.BucketTime = now.Add(time.Minute)
	write(v) // upper bound excluded.
	v = old
	v.BucketTime = now.Add(-49 * time.Hour)
	write(v)
	v = old
	v.InstanceID = site + "-other"
	write(v)
	items, asOf, err := New(db).QueryRoutineProbeCoverage(ctx, site, now, now.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.False(t, asOf.IsZero())
	require.Equal(t, int64(3), items[7].Scheduled.Due)
	require.Equal(t, int64(2), items[7].Scheduled.Sent)
	require.Equal(t, int64(1), items[7].Scheduled.GlobalLimited)
	require.NotNil(t, items[7].Scheduled.LastSkippedAt)
	require.Equal(t, int64(3), items[7].Accelerated.Due)
	require.Equal(t, int64(1), items[7].Accelerated.EvidenceLimited)
	require.NoError(t, s.PruneRoutineProbeCoverage(ctx, now.Add(-48*time.Hour)))
	items, _, err = s.QueryRoutineProbeCoverage(ctx, site, now.Add(-50*time.Hour), now)
	require.NoError(t, err)
	require.Empty(t, items)
}
