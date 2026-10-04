package tuning

import (
	"context"
	"log"
	"sync"
	"time"
)

type channelKey struct {
	site    string
	channel int64
}
type channelGuard struct {
	sync.Mutex
	version uint64
}

func (e *Engine) guard(site string, channel int64) *channelGuard {
	g, _ := e.guards.LoadOrStore(channelKey{site, channel}, &channelGuard{})
	return g.(*channelGuard)
}

func (e *Engine) context() context.Context {
	if e.ctx != nil {
		return e.ctx
	}
	return context.Background()
}

// Jobs use immutable evaluation inputs and a separate result per channel.
// Workers never grow with the number of channels, and shutdown starts no new IO.
func (e *Engine) runChannels(jobs []func()) {
	width := max(1, e.parallelism)
	if width == 1 {
		for _, job := range jobs {
			if e.context().Err() != nil {
				return
			}
			job()
		}
		return
	}
	queue := make(chan func())
	var workers sync.WaitGroup
	for i := 0; i < min(width, len(jobs)); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range queue {
				if e.context().Err() == nil {
					job()
				}
			}
		}()
	}
	for _, job := range jobs {
		if e.context().Err() != nil {
			break
		}
		select {
		case <-e.context().Done():
		case queue <- job:
		}
	}
	close(queue)
	workers.Wait()
}

// Coalesce overlapping ticks per site, while allowing other sites to run on
// schedule. Queue entries carry no old ticker timestamp or evaluation data.
func (e *Engine) runSites(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	done := make(chan string, 4)
	var workers sync.WaitGroup
	defer workers.Wait()
	queued := map[string]bool{}
	var queue []string
	running := 0
	enqueue := func() {
		ids, err := e.store.ListEnabledSites()
		if err != nil {
			log.Printf("tuning list sites failed: %v", err)
			return
		}
		for _, id := range ids {
			if !queued[id] {
				queued[id] = true
				queue = append(queue, id)
			}
		}
	}
	enqueue()
	for {
		if ctx.Err() != nil {
			return
		}
		for running < 4 && len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			running++
			workers.Add(1)
			go func() {
				defer workers.Done()
				if ctx.Err() == nil {
					e.evaluateSite(id, time.Now().UTC())
				}
				done <- id
			}()
		}
		select {
		case <-ctx.Done():
			return
		case id := <-done:
			running--
			delete(queued, id)
		case <-ticker.C:
			enqueue()
		}
	}
}
