package main

import (
	"context"
	"sync"
	"time"

	"controltower/agent/internal/reporter"
)

type probeDispatcherKey struct{}

// Probe IO has its own lifetime; a collector pass normally lasts only 5s.
// Results rejoin the normal buffered report path on the next healthy pass.
type probeDispatcher struct {
	mu      sync.Mutex
	jobs    chan reporter.ChannelCommand
	seen    map[string]time.Time
	results []reporter.ChannelCommandResult
	guards  sync.Map
}

func withProbeDispatcher(ctx context.Context, controller channelController) context.Context {
	d := &probeDispatcher{jobs: make(chan reporter.ChannelCommand, 64), seen: map[string]time.Time{}}
	for i := 0; i < 4; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case command := <-d.jobs:
					guard, _ := d.guards.LoadOrStore(command.ChannelID, &sync.Mutex{})
					lock := guard.(*sync.Mutex)
					lock.Lock()
					if ctx.Err() != nil {
						lock.Unlock()
						return
					}
					probeCtx, cancel := context.WithTimeout(ctx, 8*time.Minute)
					result := executeCommand(probeCtx, controller, command)
					cancel()
					lock.Unlock()
					d.mu.Lock()
					d.results = append(d.results, result)
					d.seen[command.ID] = time.Now()
					d.mu.Unlock()
				}
			}
		}()
	}
	return context.WithValue(ctx, probeDispatcherKey{}, d)
}

func (d *probeDispatcher) submit(command reporter.ChannelCommand) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, completedAt := range d.seen {
		if !completedAt.IsZero() && time.Since(completedAt) > time.Hour {
			delete(d.seen, id)
		}
	}
	if _, exists := d.seen[command.ID]; exists {
		return
	}
	d.seen[command.ID] = time.Time{}
	select {
	case d.jobs <- command:
	default:
		d.seen[command.ID] = time.Now()
		d.results = append(d.results, reporter.ChannelCommandResult{ID: command.ID, ChannelID: command.ChannelID, Status: "failed", Error: "probe queue full", AppliedAt: time.Now().UTC()})
	}
}

func (d *probeDispatcher) drain() []reporter.ChannelCommandResult {
	d.mu.Lock()
	defer d.mu.Unlock()
	results := d.results
	d.results = nil
	return results
}
