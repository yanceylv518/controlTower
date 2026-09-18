package dashboard

import (
	"context"
	"controltower/server/internal/storage"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

type scheduledRollupStore struct{ fakeRollupStore }

func (s *scheduledRollupStore) ListInstances() ([]storage.Instance, error) {
	return []storage.Instance{
		{SiteID: "a", Enabled: true, LogsReadonlyDSN: "configured"},
		{SiteID: "a", Enabled: true, LogsReadonlyDSN: "configured"},
		{SiteID: "b", Enabled: true, LogsReadonlyDSN: "configured"},
		{SiteID: "c", Enabled: true, LogsReadonlyDSN: "configured"},
	}, nil
}
func (s *scheduledRollupStore) ReadonlyLogRollupCursor(context.Context, string) (storage.ReadonlyLogRollupCursor, error) {
	return storage.ReadonlyLogRollupCursor{Initialized: true}, nil
}
func (s *scheduledRollupStore) MarkReadonlyLogRollupCaughtUp(context.Context, string, time.Time) error {
	return nil
}

type scheduledRollupSource struct {
	fakeRollupSource
	mu          sync.Mutex
	active, max int
	seen        map[string]int
	started     chan string
	release     chan struct{}
}

func (s *scheduledRollupSource) readonlyLogHead(ctx context.Context, site string) (int64, error) {
	s.mu.Lock()
	s.active++
	if s.active > s.max {
		s.max = s.active
	}
	s.seen[site]++
	s.mu.Unlock()
	s.started <- site
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	s.mu.Lock()
	s.active--
	s.mu.Unlock()
	return 0, ctx.Err()
}
func TestReadonlyRollupSchedulesTwoSitesWithoutDuplicateWorkers(t *testing.T) {
	s := &scheduledRollupSource{seen: map[string]int{}, started: make(chan string, 3), release: make(chan struct{})}
	r := ReadonlyLogRollupRunner{Source: s, Store: &scheduledRollupStore{}}
	done := make(chan struct{})
	go func() { r.runOnce(context.Background()); close(done) }()
	for i := 0; i < 2; i++ {
		select {
		case <-s.started:
		case <-time.After(time.Second):
			t.Fatal("sites did not run concurrently")
		}
	}
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	require.Equal(t, 2, active)
	close(s.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("round did not finish")
	}
	require.Equal(t, 2, s.max)
	require.Equal(t, map[string]int{"a": 1, "b": 1, "c": 1}, s.seen)
}
