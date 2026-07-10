package aggregator

import (
	"context"
	"sync"
)

type MemoryLock struct {
	mu     sync.Mutex
	locked bool
}

func NewMemoryLock() *MemoryLock {
	return &MemoryLock{}
}

func (l *MemoryLock) TryLock(ctx context.Context) (func(), bool, error) {
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	default:
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.locked {
		return nil, false, nil
	}
	l.locked = true
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.locked = false
	}, true, nil
}
