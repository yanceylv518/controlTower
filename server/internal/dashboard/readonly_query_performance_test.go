package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cachedTestResponse(status int) *readonlyResponseBuffer {
	b := &readonlyResponseBuffer{header: make(http.Header), status: status}
	b.WriteString("result")
	return b
}
func TestReadonlyCacheCoalescesAndExpires(t *testing.T) {
	c := &readonlyQueryCache{entries: make(map[[32]byte]*readonlyCachedQuery)}
	key := [32]byte{1}
	var calls atomic.Int32
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := c.get(context.Background(), key, func(context.Context) *readonlyResponseBuffer { calls.Add(1); <-gate; return cachedTestResponse(200) })
			assert.Equal(t, "result", result.String())
		}()
	}
	require.Eventually(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.entries[key] != nil && c.entries[key].waiters == 10
	}, time.Second, time.Millisecond)
	close(gate)
	wg.Wait()
	require.Equal(t, int32(1), calls.Load())
	run := func(context.Context) *readonlyResponseBuffer { calls.Add(1); return cachedTestResponse(200) }
	c.get(context.Background(), key, run)
	require.Equal(t, int32(1), calls.Load())
	c.mu.Lock()
	c.entries[key].expires = time.Now().Add(-time.Second)
	c.mu.Unlock()
	c.get(context.Background(), key, run)
	require.Equal(t, int32(2), calls.Load())
}
func TestReadonlyCacheCancellationAndErrors(t *testing.T) {
	c := &readonlyQueryCache{entries: make(map[[32]byte]*readonlyCachedQuery)}
	key := [32]byte{1}
	ctx, cancel := context.WithCancel(context.Background())
	started, stopped, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		c.get(ctx, key, func(ctx context.Context) *readonlyResponseBuffer {
			close(started)
			<-ctx.Done()
			close(stopped)
			return cachedTestResponse(502)
		})
	}()
	<-started
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("orphaned database query")
	}
	<-done
	var calls int
	run := func(context.Context) *readonlyResponseBuffer { calls++; return cachedTestResponse(502) }
	c.get(context.Background(), key, run)
	c.get(context.Background(), key, run)
	require.Equal(t, 2, calls, "errors must not be cached")
}
func TestReadonlyCacheOneWaiterCannotCancelOthers(t *testing.T) {
	c := &readonlyQueryCache{entries: make(map[[32]byte]*readonlyCachedQuery)}
	key := [32]byte{1}
	ctx, cancel := context.WithCancel(context.Background())
	gate, started := make(chan struct{}), make(chan struct{})
	run := func(ctx context.Context) *readonlyResponseBuffer {
		close(started)
		select {
		case <-ctx.Done():
			return cachedTestResponse(502)
		case <-gate:
			return cachedTestResponse(200)
		}
	}
	done1, done2 := make(chan *readonlyResponseBuffer, 1), make(chan *readonlyResponseBuffer, 1)
	go func() { done1 <- c.get(ctx, key, run) }()
	<-started
	go func() { done2 <- c.get(context.Background(), key, run) }()
	require.Eventually(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.entries[key].waiters == 2 }, time.Second, time.Millisecond)
	cancel()
	require.Nil(t, <-done1)
	close(gate)
	require.Equal(t, 200, (<-done2).status)
}
func TestReadonlySummaryCacheSeparatesScopeAndFilters(t *testing.T) {
	h := &PassthroughHandler{Config: customerQueryConfig{}}
	calls := 0
	run := func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{"total":3}`)) }
	base := "/?start_time=2026-09-01T00:00:00Z&end_time=2026-09-02T00:00:00Z&site=a&user_ids=1"
	for _, suffix := range []string{"", "", "&model_name=x", "&user_ids=2", "&site=b"} {
		// Set replaces existing values, as a real site/scope change would.
		r := httptest.NewRequest("GET", base, nil)
		if suffix != "" {
			extra := httptest.NewRequest("GET", "/?"+suffix[1:], nil).URL.Query()
			q := r.URL.Query()
			for k, v := range extra {
				q[k] = v
			}
			r.URL.RawQuery = q.Encode()
		}
		h.cachedReadonlySummary(httptest.NewRecorder(), r, "count", run)
	}
	require.Equal(t, 4, calls)
}
func TestReadonlyCursorPreservesTimeTieAndScope(t *testing.T) {
	args := []any{int64(10), int64(100), int64(7)}
	scope := readonlyPageScope("a", true, " AND l.user_id IN (?)", args)
	item := PassthroughLog{ID: 42, CreatedAt: time.Unix(50, 0)}
	for _, previous := range []bool{false, true} {
		cursor, err := parseReadonlyPageCursor(readonlyPageToken(item, previous, scope), scope)
		require.NoError(t, err)
		query, values := readonlyPageSQL(" AND l.user_id IN (?)", args, 20, 100000, cursor)
		assert.Contains(t, query, "l.created_at>=? AND l.created_at<? AND l.user_id IN (?)")
		if previous {
			assert.Contains(t, query, "l.id > ?")
			assert.Contains(t, query, "ORDER BY l.created_at ASC,l.id ASC")
		} else {
			assert.Contains(t, query, "l.id < ?")
			assert.Contains(t, query, "ORDER BY l.created_at DESC,l.id DESC")
		}
		assert.Equal(t, []any{int64(10), int64(100), int64(7), int64(50), int64(50), int64(42), 21, 0}, values)
	}
	_, err := parseReadonlyPageCursor(readonlyPageToken(item, false, scope), readonlyPageScope("b", true, "", args))
	require.Error(t, err)
	_, err = parseReadonlyPageCursor("malformed", scope)
	require.Error(t, err)
	_, values := readonlyPageSQL("", args, 20, 100000, nil)
	assert.Equal(t, 100000, values[len(values)-1])
}
