package tuning

import (
	"context"
	"controltower/internal/channelcontrol"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

func TestProbeRecordsObserversFinishBeforeSchedulerExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var mu sync.Mutex
	sent := map[string]bool{}
	settled := map[string]routineResult{}
	p := routineTestProber{probe: func(context.Context) (channelcontrol.ProbeResult, error) { return channelcontrol.ProbeResult{}, nil }}
	runtime := routineRuntime{dispatch: func(d routineDispatch) { mu.Lock(); defer mu.Unlock(); sent[d.id] = true }, settled: func(r routineResult) { mu.Lock(); defer mu.Unlock(); settled[r.id] = r }}
	runRoutineChannelRuntime(ctx, p, 7, "m", make(chan struct{}, 8), routineTiming{time.Hour, time.Second}, DefaultPolicy().Continuous, runtime, func(routineStreak, time.Time) bool { return false })
	require.Len(t, sent, 3)
	require.Len(t, settled, 3)
	for id, r := range settled {
		require.True(t, sent[id])
		require.Equal(t, routineError, r.outcome)
	}
}

func TestProbeRecordsBufferRetainsConcurrentTerminalAndReportsLoss(t *testing.T) {
	b := newProbeRecordBuffer()
	v := ProbeRecord{ID: "one", InstanceID: "a", StartedAt: time.Now(), Outcome: "pending"}
	b.put(v)
	pending, _ := b.snapshot()
	v.Outcome = "slow"
	b.put(v)
	b.acknowledge(pending)
	rows, _ := b.snapshot()
	require.Len(t, rows, 1)
	require.Equal(t, "slow", rows[0].Outcome)
	b.put(pending[0])
	rows, _ = b.snapshot()
	require.Equal(t, "slow", rows[0].Outcome)
	b.acknowledge(rows)
	rows, _ = b.snapshot()
	require.Empty(t, rows)
	v.ID = "expired"
	v.StartedAt = time.Now().Add(-ProbeRecordRetention - time.Hour)
	b.put(v)
	rows, health := b.snapshot()
	require.Empty(t, rows)
	require.EqualValues(t, 1, health[0].DroppedRecords)
	require.NotNil(t, health[0].LastLossAt)
}
