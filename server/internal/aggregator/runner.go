package aggregator

import (
	"context"
	"errors"
	"time"

	"controltower/server/internal/storage"
)

var ErrSchedulerLocked = errors.New("aggregation scheduler locked")

type Lock interface {
	TryLock(ctx context.Context) (func(), bool, error)
}

type EventSource interface {
	EventsForAggregation(ctx context.Context) ([]storage.LogEvent, error)
}

type Clock interface {
	After(duration time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) After(duration time.Duration) <-chan time.Time {
	return time.After(duration)
}

type Runner struct {
	scheduler Scheduler
	source    EventSource
	lock      Lock
	clock     Clock
	interval  time.Duration
	backoff   Backoff
}

type RunnerOption func(*Runner)

func NewRunner(scheduler Scheduler, source EventSource, lock Lock, interval time.Duration, options ...RunnerOption) Runner {
	runner := Runner{
		scheduler: scheduler,
		source:    source,
		lock:      lock,
		clock:     realClock{},
		interval:  interval,
		backoff:   NewBackoff(1*time.Second, 30*time.Second),
	}
	for _, option := range options {
		option(&runner)
	}
	return runner
}

func WithClock(clock Clock) RunnerOption {
	return func(r *Runner) {
		if clock != nil {
			r.clock = clock
		}
	}
}

func WithBackoff(backoff Backoff) RunnerOption {
	return func(r *Runner) {
		r.backoff = backoff
	}
}

func (r Runner) RunOnceLocked(ctx context.Context) (RunResult, error) {
	if r.lock == nil {
		return r.runOnce(ctx)
	}
	unlock, locked, err := r.lock.TryLock(ctx)
	if err != nil {
		return RunResult{}, err
	}
	if !locked {
		return RunResult{}, ErrSchedulerLocked
	}
	defer unlock()
	return r.runOnce(ctx)
}

func (r Runner) runOnce(ctx context.Context) (RunResult, error) {
	if r.source == nil {
		return RunResult{}, errors.New("aggregation event source not configured")
	}
	events, err := r.source.EventsForAggregation(ctx)
	if err != nil {
		return RunResult{}, err
	}
	return r.scheduler.RunOnce(events)
}

func (r Runner) Run(ctx context.Context) error {
	if r.interval <= 0 {
		return errors.New("aggregation interval must be positive")
	}
	failureCount := 0
	for {
		_, err := r.RunOnceLocked(ctx)
		if err == nil || errors.Is(err, ErrSchedulerLocked) {
			failureCount = 0
		} else {
			failureCount++
		}

		delay := r.interval
		if failureCount > 0 {
			delay = r.backoff.Delay(failureCount)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.clock.After(delay):
		}
	}
}
