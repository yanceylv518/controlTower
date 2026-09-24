package mysqlstore

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"controltower/server/internal/storage"
)

const auditCountTTL = 30 * time.Second

type auditCountEntry struct {
	total   int64
	expires time.Time
}

// Per-store, bounded cache. One count runs at a time so background totals cannot
// consume the connection pool; waiting callers remain cancellable.
type auditCountCache struct {
	mu      sync.Mutex
	entries map[string]auditCountEntry
	gate    chan struct{}
}

func newAuditCountCache() *auditCountCache {
	return &auditCountCache{entries: make(map[string]auditCountEntry), gate: make(chan struct{}, 1)}
}

func (c *auditCountCache) get(key string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.entries[key]
	return v.total, ok && time.Now().Before(v.expires)
}

func (c *auditCountCache) count(ctx context.Context, q storage.OperationAuditQuery, query func() (int64, error)) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if c == nil {
		return query()
	}
	q.Limit, q.Offset, q.BeforeID, q.BeforeTime = 0, 0, "", time.Time{}
	keyBytes, _ := json.Marshal(q)
	key := string(keyBytes)
	if total, ok := c.get(key); ok {
		return total, nil
	}
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	if total, ok := c.get(key); ok {
		return total, nil
	}
	total, err := query()
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= 128 {
		clear(c.entries)
	}
	c.entries[key] = auditCountEntry{total: total, expires: time.Now().Add(auditCountTTL)}
	return total, nil
}
