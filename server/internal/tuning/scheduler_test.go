package tuning

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// IO hooks intentionally block outside the mutex, like an independent slow
// upstream. The fake's state storage itself is safe under the race detector.
type schedulerStore struct {
	continuousFake
	mu           sync.Mutex
	statesByKey  map[channelKey]ContinuousState
	writeStarted chan Recommendation
	writeHook    func(Recommendation)
	stateHook    func()
	policyHook   func(string)
	sites        []string
}

func schedulerFixture() *schedulerStore {
	p := autoPolicy()
	p.Policy.DispatchModes["urgent"] = "auto"
	f := &schedulerStore{statesByKey: map[channelKey]ContinuousState{}, writeStarted: make(chan Recommendation, 32), sites: []string{"i"}}
	f.policy = &p
	for i := int64(1); i <= 3; i++ {
		f.bases = append(f.bases, ChannelBaseValue{ChannelID: i, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 50})
		f.metrics = append(f.metrics, ChannelMetric{ChannelID: i, RequestCount: 30, TTFTP50: 1, TTFTP90: 1, TTFTP95: 1, SpeedSamples: 30, SpeedTTFTP50: 1, SpeedTTFTP90: 1, SpeedTTFTP95: 1})
	}
	f.bases[2].ModelName, f.bases[2].Models, f.bases[2].CurrentWeight = "urgent", []string{"urgent"}, 100
	return f
}

func (f *schedulerStore) ListEnabledSites() ([]string, error) { return f.sites, nil }
func (f *schedulerStore) GetPolicy(site string) (PolicyRecord, bool, error) {
	if f.policyHook != nil {
		f.policyHook(site)
	}
	p := *f.policy
	p.InstanceID = site
	return p, true, nil
}
func (f *schedulerStore) QueryMetrics(string, time.Time, time.Time) ([]ChannelMetric, error) {
	return f.metrics, nil
}
func (f *schedulerStore) ListChannelBaseValues(string, string) ([]ChannelBaseValue, error) {
	return append([]ChannelBaseValue(nil), f.bases...), nil
}
func (f *schedulerStore) ListContinuousStates(site string) ([]ContinuousState, error) {
	f.mu.Lock()
	var states []ContinuousState
	for key, s := range f.statesByKey {
		if key.site == site {
			states = append(states, s)
		}
	}
	f.mu.Unlock()
	if f.stateHook != nil {
		f.stateHook()
	}
	return states, nil
}
func (f *schedulerStore) PutContinuousState(s ContinuousState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statesByKey[channelKey{s.InstanceID, s.ChannelID}] = s
	return nil
}
func (f *schedulerStore) InsertRecommendation(Recommendation) error { return nil }
func (f *schedulerStore) CreateContinuousWeightChange(r Recommendation, _ string, _ time.Time) (string, error) {
	f.writeStarted <- r
	if f.writeHook != nil {
		f.writeHook(r)
	}
	return "", nil
}
func (f *schedulerStore) state(channel int64) ContinuousState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statesByKey[channelKey{"i", channel}]
}

func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for worker")
	}
}
func waitWrite(t *testing.T, ch <-chan Recommendation, channel int64, rule string) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case r := <-ch:
			if r.ChannelID == channel && r.Rule == rule {
				return
			}
		case <-timer.C:
			t.Fatalf("channel %d %s blocked", channel, rule)
		}
	}
}

func TestRuntimeSlowWriteDoesNotBlockPeerOrFastCircuit(t *testing.T) {
	f := schedulerFixture()
	blocked, release := make(chan struct{}), make(chan struct{})
	f.writeHook = func(r Recommendation) {
		if r.ChannelID == 1 && r.Rule == "weight_write" {
			close(blocked)
			<-release
		}
	}
	e := NewEngine(f)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = e.Run(ctx) }()
	t.Cleanup(func() { cancel(); close(release); waitSignal(t, done) })
	waitSignal(t, blocked)
	waitWrite(t, f.writeStarted, 2, "weight_write")
	now := time.Now().UTC()
	e.SubmitFastCircuitBatch(FastCircuitBatch{InstanceID: "inst", ReportedAt: now, Metrics: []FastCircuitMetric{{ChannelID: 3, RequestCount: 50, ErrorCount: 50}}})
	waitWrite(t, f.writeStarted, 3, "circuit_opened")
}

func TestFastCircuitCannotBeOverwrittenByOlderNormalSnapshot(t *testing.T) {
	f := schedulerFixture()
	snapshot, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	f.stateHook = func() {
		if reads.Add(1) == 1 {
			close(snapshot)
			<-release
		}
	}
	e := NewEngine(f)
	e.parallelism = 4
	done := make(chan struct{})
	go func() { defer close(done); e.evaluateContinuous("i", *f.policy, time.Now().UTC(), f) }()
	defer func() {
		close(release)
		waitSignal(t, done)
		if f.state(1).Phase != "circuit" {
			t.Error("old normal snapshot overwrote circuit")
		}
	}()
	waitSignal(t, snapshot)
	e.evaluateFastCircuit(FastCircuitBatch{InstanceID: "inst", ReportedAt: time.Now().UTC(), Metrics: []FastCircuitMetric{{ChannelID: 1, RequestCount: 50, ErrorCount: 50}}}, time.Now().UTC())
	if f.state(1).Phase != "circuit" {
		t.Fatal("fast path did not execute while normal snapshot was blocked")
	}
}

func TestSameChannelFastCircuitWaitsForInFlightWriteAndUsesLatestState(t *testing.T) {
	f := schedulerFixture()
	blocked, release := make(chan struct{}), make(chan struct{})
	f.writeHook = func(r Recommendation) {
		if r.ChannelID == 1 && r.Rule == "weight_write" {
			close(blocked)
			<-release
		}
	}
	e := NewEngine(f)
	e.parallelism = 4
	normalDone, fastDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(normalDone); e.evaluateContinuous("i", *f.policy, time.Now().UTC(), f) }()
	waitSignal(t, blocked)
	go func() {
		defer close(fastDone)
		e.evaluateFastCircuit(FastCircuitBatch{InstanceID: "inst", ReportedAt: time.Now().UTC(), Metrics: []FastCircuitMetric{{ChannelID: 1, RequestCount: 50, ErrorCount: 50}}}, time.Now().UTC())
	}()
	select {
	case <-fastDone:
		t.Error("same-channel circuit overtook unfinished write")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	waitSignal(t, normalDone)
	waitSignal(t, fastDone)
	s := f.state(1)
	if s.Phase != "circuit" || s.LastWrittenWeight == nil || *s.LastWrittenWeight != 0 {
		t.Fatalf("final write must be zero: %+v", s)
	}
}

func TestSiteSchedulerDoesNotOverlapSlowSiteOrDelayHealthyNextTick(t *testing.T) {
	f := schedulerFixture()
	f.sites = []string{"slow", "healthy"}
	f.bases = nil
	blocked, release := make(chan struct{}), make(chan struct{})
	var slowCalls, healthyCalls atomic.Int32
	healthyTwice := make(chan struct{})
	f.policyHook = func(site string) {
		if site == "slow" {
			if slowCalls.Add(1) == 1 {
				close(blocked)
			}
			<-release
		} else if healthyCalls.Add(1) == 2 {
			close(healthyTwice)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := NewEngine(f)
	e.ctx = ctx
	done := make(chan struct{})
	go func() { defer close(done); e.runSites(ctx, 10*time.Millisecond) }()
	t.Cleanup(func() { cancel(); close(release); waitSignal(t, done) })
	waitSignal(t, blocked)
	waitSignal(t, healthyTwice)
	if slowCalls.Load() != 1 {
		t.Fatal("slow site overlapped itself")
	}
}

type receiptTimeStore struct {
	continuousFake
	confirmed time.Time
}

func TestChannelWorkersAreBoundedAndCancelQueuedWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := NewEngine(schedulerFixture())
	e.ctx = ctx
	e.parallelism = 4
	started := make(chan struct{}, 20)
	release := make(chan struct{})
	done := make(chan struct{})
	var jobs []func()
	for i := 0; i < 20; i++ {
		jobs = append(jobs, func() { started <- struct{}{}; <-release })
	}
	go func() { defer close(done); e.runChannels(jobs) }()
	for i := 0; i < 4; i++ {
		waitSignal(t, started)
	}
	select {
	case <-started:
		t.Fatal("worker limit exceeded")
	default:
	}
	cancel()
	close(release)
	waitSignal(t, done)
	if len(started) != 0 {
		t.Fatal("started queued work after shutdown")
	}
}

func (f *receiptTimeStore) ContinuousCommandResult(string) (string, time.Time, error) {
	return "succeeded", f.confirmed, nil
}

func TestLateObservationDoesNotRestartConfirmedFeedbackOrSilentClock(t *testing.T) {
	now := time.Now().UTC()
	confirmed := now.Add(-25 * time.Second)
	f := &receiptTimeStore{confirmed: confirmed}
	s := ContinuousState{Phase: "circuit", Capacity: CapacityControl{PendingCommandID: "done", PendingWeight: 0, Active: true, Fresh: true}}
	NewEngine(f).settleCapacityWrite(f, "i", ChannelBaseValue{}, &s, now)
	if !s.Capacity.AppliedAt.Equal(confirmed) || !s.NextProbeAt.Equal(confirmed.Add(5*time.Minute)) {
		t.Fatalf("engine poll restarted confirmation clock: %+v", s)
	}
}

func TestLongTuningEventIdentityFitsStorageWithoutLosingDistinctness(t *testing.T) {
	now := time.Now().UTC()
	site := strings.Repeat("a", 64)
	a := NewID(now, site, 1, "circuit_opened")
	if len(a) > 64 || a != NewID(now, site, 1, "circuit_opened") {
		t.Fatalf("unstable or oversized id: %q", a)
	}
	for _, b := range []string{NewID(now, site, 2, "circuit_opened"), NewID(now, site, 1, "circuit_recovered"), NewID(now, site+"b", 1, "circuit_opened"), NewID(now.Add(time.Nanosecond), site, 1, "circuit_opened")} {
		if len(b) > 64 || a == b {
			t.Fatalf("different event identity lost: %q", b)
		}
	}
}
