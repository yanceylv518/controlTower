package aggregator

import (
	"context"
	"errors"
	"testing"
	"time"

	"controltower/server/internal/storage"
)

func TestMemoryLockAllowsOnlyOneOwner(t *testing.T) {
	lock := NewMemoryLock()
	unlock, ok, err := lock.TryLock(context.Background())
	if err != nil {
		t.Fatalf("first lock error: %v", err)
	}
	if !ok {
		t.Fatal("first lock not acquired")
	}
	_, ok, err = lock.TryLock(context.Background())
	if err != nil {
		t.Fatalf("second lock error: %v", err)
	}
	if ok {
		t.Fatal("second lock acquired while first owner still holds it")
	}
	unlock()
	_, ok, err = lock.TryLock(context.Background())
	if err != nil {
		t.Fatalf("third lock error: %v", err)
	}
	if !ok {
		t.Fatal("third lock not acquired after unlock")
	}
}

func TestRunnerRunOnceLockedAggregatesFromSource(t *testing.T) {
	store := NewMemoryMetricStore()
	source := &staticEventSource{events: []storage.LogEvent{{
		InstanceID:  "inst-1",
		SourceLogID: 1,
		CreatedAt:   time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		LogType:     "consume",
		ModelName:   "gpt-4o",
		TotalTokens: 10,
		Quota:       20,
	}}}
	runner := NewRunner(NewScheduler(store), source, NewMemoryLock(), time.Minute)

	result, err := runner.RunOnceLocked(context.Background())
	if err != nil {
		t.Fatalf("RunOnceLocked returned error: %v", err)
	}
	if result.Events != 1 || result.Metrics1m == 0 || result.Metrics5m == 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(store.Recent1mMetrics()) == 0 || len(store.Recent5mMetrics()) == 0 {
		t.Fatal("metrics not stored")
	}
}

func TestRunnerRunOnceLockedSkipsWhenLocked(t *testing.T) {
	lock := NewMemoryLock()
	unlock, ok, err := lock.TryLock(context.Background())
	if err != nil || !ok {
		t.Fatalf("pre-lock failed ok=%v err=%v", ok, err)
	}
	defer unlock()
	source := &staticEventSource{events: []storage.LogEvent{{InstanceID: "inst-1", SourceLogID: 1, CreatedAt: time.Now()}}}
	runner := NewRunner(NewScheduler(NewMemoryMetricStore()), source, lock, time.Minute)

	_, err = runner.RunOnceLocked(context.Background())
	if !errors.Is(err, ErrSchedulerLocked) {
		t.Fatalf("error = %v, want ErrSchedulerLocked", err)
	}
	if source.calls != 0 {
		t.Fatalf("source called while locked: %d", source.calls)
	}
}

func TestBackoffDelayDoublesAndCaps(t *testing.T) {
	backoff := NewBackoff(2*time.Second, 10*time.Second)
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, 0},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 10 * time.Second},
		{5, 10 * time.Second},
	}
	for _, tc := range cases {
		if got := backoff.Delay(tc.failures); got != tc.want {
			t.Fatalf("Delay(%d) = %s, want %s", tc.failures, got, tc.want)
		}
	}
}

func TestRunnerRunUsesBackoffAfterFailureAndIntervalAfterSuccess(t *testing.T) {
	store := NewMemoryMetricStore()
	source := &sequenceEventSource{
		responses: []eventSourceResponse{
			{err: errors.New("temporary query failure")},
			{},
		},
	}
	clock := newManualClock()
	ctx, cancel := context.WithCancel(context.Background())
	runner := NewRunner(
		NewScheduler(store),
		source,
		NewMemoryLock(),
		time.Minute,
		WithClock(clock),
		WithBackoff(NewBackoff(time.Second, 4*time.Second)),
	)

	done := make(chan error, 1)
	go func() {
		done <- runner.Run(ctx)
	}()

	firstWait := clock.next(t)
	if firstWait.duration != time.Second {
		t.Fatalf("first wait = %s, want backoff 1s", firstWait.duration)
	}
	close(firstWait.tick)

	secondWait := clock.next(t)
	if secondWait.duration != time.Minute {
		t.Fatalf("second wait = %s, want normal interval", secondWait.duration)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context canceled", err)
	}
	if source.calls != 2 {
		t.Fatalf("source calls = %d, want 2", source.calls)
	}
}

type staticEventSource struct {
	events []storage.LogEvent
	err    error
	calls  int
}

func (s *staticEventSource) EventsForAggregation(ctx context.Context) ([]storage.LogEvent, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return append([]storage.LogEvent(nil), s.events...), nil
}

type eventSourceResponse struct {
	events []storage.LogEvent
	err    error
}

type sequenceEventSource struct {
	responses []eventSourceResponse
	calls     int
}

func (s *sequenceEventSource) EventsForAggregation(ctx context.Context) ([]storage.LogEvent, error) {
	if s.calls >= len(s.responses) {
		s.calls++
		return nil, nil
	}
	response := s.responses[s.calls]
	s.calls++
	if response.err != nil {
		return nil, response.err
	}
	return append([]storage.LogEvent(nil), response.events...), nil
}

type manualClock struct {
	requests chan clockRequest
}

type clockRequest struct {
	duration time.Duration
	tick     chan time.Time
}

func newManualClock() *manualClock {
	return &manualClock{requests: make(chan clockRequest, 4)}
}

func (c *manualClock) After(duration time.Duration) <-chan time.Time {
	tick := make(chan time.Time)
	c.requests <- clockRequest{duration: duration, tick: tick}
	return tick
}

func (c *manualClock) next(t *testing.T) clockRequest {
	t.Helper()
	select {
	case request := <-c.requests:
		return request
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for clock request")
	}
	return clockRequest{}
}
