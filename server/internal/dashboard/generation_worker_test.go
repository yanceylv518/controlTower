package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"sync"
	"testing"
	"time"
)

type generationQueueFake struct {
	mu        sync.Mutex
	jobs      map[string][]billing.QueuedGeneration
	locked    map[string]bool
	duplicate bool
}

func (s *generationQueueFake) GenerationSites(context.Context) ([]string, error) {
	return []string{"a", "b"}, nil
}
func (s *generationQueueFake) LockGenerationSite(ctx context.Context, site string) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locked[site] {
		s.duplicate = true
		return nil, nil, billing.ErrGenerationInProgress
	}
	s.locked[site] = true
	return ctx, func() { s.mu.Lock(); delete(s.locked, site); s.mu.Unlock() }, nil
}
func (s *generationQueueFake) NextGeneration(_ context.Context, site string) (billing.QueuedGeneration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.jobs[site]) == 0 {
		return billing.QueuedGeneration{}, sql.ErrNoRows
	}
	return s.jobs[site][0], nil
}
func TestSiteGenerationWorkerIsolationAndShutdown(t *testing.T) {
	s := &generationQueueFake{jobs: map[string][]billing.QueuedGeneration{"a": {{Kind: "report", ID: "a-report"}, {Kind: "billing", ID: "a-bill"}}, "b": {{Kind: "billing", ID: "b-bill"}}}, locked: map[string]bool{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan string, 4)
	releaseA := make(chan struct{})
	done := make(chan struct{})
	w := SiteGenerationWorker{Store: s, Poll: 5 * time.Millisecond, RunTask: func(ctx context.Context, site string, task billing.QueuedGeneration) (bool, error) {
		started <- task.ID
		if task.ID == "a-report" {
			select {
			case <-releaseA:
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}
		if task.ID == "a-bill" {
			<-ctx.Done()
			return false, ctx.Err()
		}
		s.mu.Lock()
		s.jobs[site] = s.jobs[site][1:]
		s.mu.Unlock()
		return true, nil
	}}
	go func() { w.Run(ctx); close(done) }()
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case id := <-started:
			seen[id] = true
		case <-time.After(3 * time.Second):
			t.Fatal("site b blocked by site a")
		}
	}
	if !seen["a-report"] || !seen["b-bill"] {
		t.Fatal("same-site jobs overlapped", seen)
	}
	select {
	case id := <-started:
		t.Fatal("second a task started early", id)
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseA)
	select {
	case id := <-started:
		if id != "a-bill" {
			t.Fatal(id)
		}
	case <-time.After(time.Second):
		t.Fatal("queue did not advance")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown left worker running")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.locked) != 0 || s.duplicate {
		t.Fatal("site ownership leaked or overlapped", s.locked, s.duplicate)
	}
}
