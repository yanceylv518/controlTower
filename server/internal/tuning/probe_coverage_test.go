package tuning

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"controltower/internal/channelcontrol"
)

func TestRoutineStaggerStableDistributedAndCancellation(t *testing.T) {
	interval := 10 * time.Second
	bins := map[int]bool{}
	for id := int64(1); id <= 100; id++ {
		d := routineInitialDelay("site", id, interval)
		if d < 0 || d >= interval || d != routineInitialDelay("site", id, interval) {
			t.Fatalf("invalid initial phase %v", d)
		}
		bins[int(d/time.Second)] = true
	}
	if len(bins) < 8 {
		t.Fatalf("startup phases cluster in only %d seconds", len(bins))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	p := routineTestProber{probe: func(context.Context) (channelcontrol.ProbeResult, error) {
		t.Error("probe started during initial delay")
		return channelcontrol.ProbeResult{}, nil
	}}
	runRoutineChannelRuntime(ctx, p, 1, "m", make(chan struct{}, 8), routineTiming{time.Second, time.Second}, DefaultPolicy().Continuous, routineRuntime{initialDelay: time.Hour, dispatch: func(routineDispatch) { t.Error("initial delay counted as due") }}, func(routineStreak, time.Time) bool { return true })
}

func TestRoutineStaggerPreservesCadenceAndErrorAcceleration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	var calls atomic.Int32
	p := routineTestProber{probe: func(context.Context) (channelcontrol.ProbeResult, error) {
		return channelcontrol.ProbeResult{Success: calls.Add(1) == 1}, nil
	}}
	var dispatches []routineDispatch
	runtime := routineRuntime{initialDelay: 30 * time.Millisecond, dispatch: func(d routineDispatch) { dispatches = append(dispatches, d) }}
	runRoutineChannelRuntime(ctx, p, 1, "m", make(chan struct{}, 8), routineTiming{150 * time.Millisecond, time.Second}, DefaultPolicy().Continuous, runtime, func(routineStreak, time.Time) bool { return false })
	if len(dispatches) != 4 || !dispatches[0].scheduled || !dispatches[1].scheduled || dispatches[2].scheduled || dispatches[3].scheduled {
		t.Fatalf("dispatches=%+v", dispatches)
	}
	if dispatches[0].at.Sub(start) < 25*time.Millisecond || dispatches[1].at.Sub(dispatches[0].at) < 140*time.Millisecond {
		t.Fatal("initial delay or normal interval skipped")
	}
	if dispatches[3].at.Sub(dispatches[1].at) > 100*time.Millisecond {
		t.Fatal("errors waited for the normal interval")
	}
}

func TestRoutineCoverageReportsCapacityReasonsWithoutInventingFailures(t *testing.T) {
	for _, kind := range []string{probeSkipGlobal, probeSkipChannel, probeSkipEvidence} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			release := make(chan struct{})
			defer close(release)
			slots := make(chan struct{}, 128)
			if kind == probeSkipGlobal {
				for i := 0; i < cap(slots); i++ {
					slots <- struct{}{}
				}
			}
			var calls atomic.Int32
			p := routineTestProber{probe: func(context.Context) (channelcontrol.ProbeResult, error) {
				n := calls.Add(1)
				if kind == probeSkipChannel || n == 1 {
					<-release
					return channelcontrol.ProbeResult{Success: true}, nil
				}
				// Two errors then one success accelerate many requests behind the
				// unresolved first request, without meeting either fault threshold.
				return channelcontrol.ProbeResult{Success: (n-1)%3 == 0}, nil
			}}
			policy := DefaultPolicy().Continuous
			timing := routineTiming{10 * time.Millisecond, 30 * time.Millisecond}
			if kind == probeSkipChannel {
				policy.ProbeSlowLimit = 100
			}
			if kind == probeSkipEvidence {
				timing = routineTiming{15 * time.Millisecond, 500 * time.Millisecond}
			}
			var dispatches []routineDispatch
			runtime := routineRuntime{dispatch: func(d routineDispatch) {
				dispatches = append(dispatches, d)
				if d.skipReason == kind {
					cancel()
				}
			}}
			var outcomes int
			runRoutineChannelRuntime(ctx, p, 7, "m", slots, timing, policy, runtime, func(routineStreak, time.Time) bool { t.Error("capacity skip tripped circuit"); return false }, func(routineResult) { outcomes++ })
			if len(dispatches) == 0 || dispatches[len(dispatches)-1].skipReason != kind {
				t.Fatalf("missing reason %s: %+v", kind, dispatches)
			}
			if kind == probeSkipGlobal && (calls.Load() != 0 || outcomes != 0) {
				t.Fatal("unsent probe became result/speed evidence")
			}
		})
	}
}

type coverageWriterFake struct {
	*continuousFake
	err    error
	writes []ProbeCoverageBucket
}

func (s *coverageWriterFake) StoreRoutineProbeCoverage(_ context.Context, v []ProbeCoverageBucket) error {
	s.writes = v
	return s.err
}
func (*coverageWriterFake) PruneRoutineProbeCoverage(context.Context, time.Time) error { return nil }

func TestProbeCoverageRetriesAndPreservesConcurrentIncrements(t *testing.T) {
	b := newProbeCoverageBuffer()
	now := time.Now().UTC().Truncate(time.Minute).Add(10 * time.Second)
	key := channelKey{"s", 7}
	b.record(key, routineDispatch{at: now, scheduled: true})
	b.record(key, routineDispatch{at: now.Add(time.Second), scheduled: true, skipReason: probeSkipGlobal})
	s := &coverageWriterFake{err: errors.New("database unavailable")}
	e := &Engine{store: s, probeCoverage: b}
	e.flushProbeCoverage(context.Background())
	if len(b.snapshot(now)) != 1 {
		t.Fatal("failed persistence discarded counters")
	}
	before := b.snapshot(now)
	b.record(key, routineDispatch{at: now.Add(2 * time.Second), scheduled: true, skipReason: probeSkipChannel})
	b.acknowledge(before)
	if got := b.snapshot(now); len(got) != 1 || got[0].Due != 3 || got[0].Sent != 1 || got[0].GlobalLimited != 1 || got[0].ChannelLimited != 1 {
		t.Fatalf("concurrent increment lost: %+v", got)
	}
	s.err = nil
	e.flushProbeCoverage(context.Background())
	if len(b.snapshot(now)) != 0 {
		t.Fatal("saved counts still dirty")
	}
	// An unchanged channel at a minute boundary must keep its absolute base.
	b.snapshot(now.Add(time.Minute))
	b.record(key, routineDispatch{at: now.Add(3 * time.Second), scheduled: true, skipReason: probeSkipEvidence})
	b.record(key, routineDispatch{at: now.Add(3 * time.Second), scheduled: false})
	rows := b.snapshot(now)
	if len(rows) != 2 {
		t.Fatalf("scheduled and accelerated mixed: %+v", rows)
	}
	for _, v := range rows {
		if v.Scheduled && (v.Due != 4 || v.Sent+v.GlobalLimited+v.ChannelLimited+v.EvidenceLimited != v.Due) {
			t.Fatalf("count invariant broken: %+v", v)
		}
	}
}
