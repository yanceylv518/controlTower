package mysqlstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"controltower/server/internal/storage"
)

func TestAuditCountCacheFiltersExpiryAndFailures(t *testing.T) {
	c := newAuditCountCache()
	calls := 0
	query := func() (int64, error) { calls++; return int64(calls), nil }
	q := storage.OperationAuditQuery{CountOnly: true, Actor: "one"}
	first, _ := c.count(context.Background(), q, query)
	q.Limit, q.Offset = 20, 200
	second, _ := c.count(context.Background(), q, query)
	if first != second || calls != 1 {
		t.Fatalf("pagination missed cache: %d %d calls=%d", first, second, calls)
	}
	q.Actor = "two"
	_, _ = c.count(context.Background(), q, query)
	if calls != 2 {
		t.Fatal("filters shared cache")
	}
	for key, entry := range c.entries {
		entry.expires = time.Now().Add(-time.Second)
		c.entries[key] = entry
	}
	_, _ = c.count(context.Background(), q, query)
	if calls != 3 {
		t.Fatal("expired total reused")
	}
	q.Actor = "failure"
	_, err := c.count(context.Background(), q, func() (int64, error) { return 0, errors.New("count failed") })
	if err == nil {
		t.Fatal("error swallowed")
	}
	_, _ = c.count(context.Background(), q, query)
	if calls != 4 {
		t.Fatal("failure was cached")
	}
}

func TestAuditCountCacheWaitCancellation(t *testing.T) {
	c := newAuditCountCache()
	c.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := c.count(ctx, storage.OperationAuditQuery{}, func() (int64, error) { t.Fatal("count started while gate held"); return 0, nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting count did not cancel: %v", err)
	}
}
