package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"errors"
	"log"
	"sync"
	"time"
)

// SiteGenerationWorker drains one queue per site. A database session lock
// coordinates different Server processes as well as billing and reports.
type SiteGenerationWorker struct {
	Store   billing.GenerationQueueStore
	Recover func(context.Context, string) error
	RunTask func(context.Context, string, billing.QueuedGeneration) (bool, error)
	Poll    time.Duration
}

func (w SiteGenerationWorker) Run(ctx context.Context) {
	interval := w.Poll
	if interval <= 0 {
		interval = time.Second
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	active := map[string]bool{}
	var mu sync.Mutex
	for ctx.Err() == nil {
		sites, err := w.Store.GenerationSites(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("generation sites: %v", err)
		}
		for _, site := range sites {
			mu.Lock()
			if active[site] {
				mu.Unlock()
				continue
			}
			active[site] = true
			mu.Unlock()
			wg.Add(1)
			go func(site string) {
				defer wg.Done()
				defer func() { mu.Lock(); delete(active, site); mu.Unlock() }()
				w.runSite(ctx, site)
			}(site)
		}
		reportPause(ctx, interval)
	}
}

func (w SiteGenerationWorker) runSite(ctx context.Context, site string) {
	lease, release, err := w.Store.LockGenerationSite(ctx, site)
	if err != nil {
		return
	}
	defer release()
	if w.Recover != nil {
		if err = w.Recover(lease, site); err != nil {
			return
		}
	}
	for lease.Err() == nil {
		task, err := w.Store.NextGeneration(lease, site)
		if errors.Is(err, sql.ErrNoRows) {
			return
		}
		if err != nil {
			log.Printf("generation queue site=%s: %v", site, err)
			return
		}
		worked, err := w.RunTask(lease, site, task)
		if err != nil || !worked {
			if err != nil && lease.Err() == nil {
				log.Printf("generation site=%s kind=%s: %v", site, task.Kind, err)
			}
			return
		}
	}
}
