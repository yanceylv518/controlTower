package dashboard

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"controltower/server/internal/storage"
	"github.com/stretchr/testify/require"
)

type reviewQuery func(context.Context, string, []driver.NamedValue) (driver.Rows, error)
type reviewConnector struct{ query reviewQuery }

func (c reviewConnector) Connect(context.Context) (driver.Conn, error) {
	return &reviewConn{c.query}, nil
}
func (c reviewConnector) Driver() driver.Driver { return customerQueryDriver{} }

type reviewConn struct{ query reviewQuery }

func (c *reviewConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *reviewConn) Close() error              { return nil }
func (c *reviewConn) Begin() (driver.Tx, error) { return channelCacheTx{}, nil }
func (c *reviewConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return channelCacheTx{}, nil
}
func (c *reviewConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.query(ctx, q, args)
}

type reviewRows struct {
	columns []string
	values  [][]driver.Value
	index   int
	failure error
}

func (r *reviewRows) Columns() []string { return r.columns }
func (r *reviewRows) Close() error      { return nil }
func (r *reviewRows) Next(dest []driver.Value) error {
	if r.index == len(r.values) {
		if r.failure != nil {
			return r.failure
		}
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

type reviewAudit struct {
	mu    sync.Mutex
	items []storage.OperationAudit
}

func (a *reviewAudit) InsertOperationAudit(v storage.OperationAudit) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.items = append(a.items, v)
	return nil
}
func (a *reviewAudit) snapshot() []storage.OperationAudit {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]storage.OperationAudit(nil), a.items...)
}

const reviewURL = "/?site=a&start_time=2026-09-01T00:00:00Z&end_time=2026-09-02T00:00:00Z"

func TestReadonlySummaryAuditOncePerCallerIncludingHitAndFailure(t *testing.T) {
	for _, kind := range []string{"count", "stat"} {
		t.Run(kind, func(t *testing.T) {
			var queries atomic.Int32
			db := sql.OpenDB(reviewConnector{func(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
				queries.Add(1)
				if strings.Contains(q, "COUNT(*),COALESCE") {
					return &reviewRows{columns: []string{"rpm", "tpm"}, values: [][]driver.Value{{int64(2), int64(8)}}}, nil
				}
				return &reviewRows{columns: []string{"value"}, values: [][]driver.Value{{int64(7)}}}, nil
			}})
			defer db.Close()
			audit := &reviewAudit{}
			h := &PassthroughHandler{Config: customerQueryConfig{}, Audit: audit, pools: map[string]passthroughPool{"a": {encrypted: "test-pool", db: db}}}
			handler := h.LogCount
			if kind == "stat" {
				handler = h.LogStat
			}
			for i := 0; i < 2; i++ {
				w := httptest.NewRecorder()
				handler(w, httptest.NewRequest("GET", reviewURL, nil))
				require.Equal(t, 200, w.Code)
			}
			wantQueries := int32(1)
			if kind == "stat" {
				wantQueries = 2
			}
			require.Equal(t, wantQueries, queries.Load())
			items := audit.snapshot()
			require.Len(t, items, 2)
			for _, item := range items {
				require.Equal(t, "succeeded", item.Status)
				require.Equal(t, "passthrough.logs."+kind, item.OperationType)
			}
			w := httptest.NewRecorder()
			handler(w, httptest.NewRequest("GET", reviewURL+"&channel_id=bad", nil))
			require.Equal(t, 400, w.Code)
			items = audit.snapshot()
			require.Len(t, items, 3)
			require.Equal(t, "failed", items[2].Status)
			require.Contains(t, items[2].AfterSummary, `"http_status":400`)
		})
	}
}
func TestReadonlySummaryAuditCoalescedAndCancelledCallers(t *testing.T) {
	audit := &reviewAudit{}
	h := &PassthroughHandler{Config: customerQueryConfig{}, Audit: audit}
	started, gate := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	run := func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(started)
		select {
		case <-gate:
			w.Write([]byte(`{"total":7}`))
		case <-r.Context().Done():
			w.WriteHeader(502)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done1, done2 := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done1)
		h.cachedReadonlySummary(httptest.NewRecorder(), httptest.NewRequest("GET", reviewURL, nil).WithContext(ctx), "count", run)
	}()
	<-started
	go func() {
		defer close(done2)
		h.cachedReadonlySummary(httptest.NewRecorder(), httptest.NewRequest("GET", reviewURL, nil), "count", run)
	}()
	require.Eventually(t, func() bool {
		h.summaryCache.mu.Lock()
		defer h.summaryCache.mu.Unlock()
		for _, e := range h.summaryCache.entries {
			if e.waiters == 2 {
				return true
			}
		}
		return false
	}, time.Second, time.Millisecond)
	cancel()
	<-done1
	close(gate)
	<-done2
	require.Equal(t, int32(1), calls.Load())
	items := audit.snapshot()
	require.Len(t, items, 2)
	require.Equal(t, "failed", items[0].Status)
	require.Contains(t, items[0].AfterSummary, `"http_status":499`)
	require.Equal(t, "succeeded", items[1].Status)
}
func TestReadonlyFallbackLookupDistinguishesUnknownFromNone(t *testing.T) {
	for _, mode := range []string{"complete", "timeout", "partial"} {
		t.Run(mode, func(t *testing.T) {
			db := sql.OpenDB(reviewConnector{func(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
				if mode == "timeout" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				if strings.Contains(q, "SELECT id,COALESCE(request_id") {
					rows := &reviewRows{columns: []string{"id", "request_id", "user_id", "type", "channel_id", "created_at", "other"}, values: [][]driver.Value{
						{int64(21), "retry", int64(7), int64(5), int64(141), int64(10), `{"admin_info":{"use_channel":["141"]}}`},
						{int64(22), "retry", int64(7), int64(2), int64(148), int64(20), `{"admin_info":{"use_channel":["141","148"]}}`},
					}}
					if mode == "partial" {
						rows.failure = errors.New("read interrupted")
					}
					return rows, nil
				}
				rows := &reviewRows{columns: []string{"request_id", "user_id", "count"}, values: [][]driver.Value{{"one", int64(7), int64(1)}, {"retry", int64(7), int64(2)}}}
				if mode == "partial" {
					rows.failure = errors.New("read interrupted")
				}
				return rows, nil
			}})
			defer db.Close()
			tx, err := db.Begin()
			require.NoError(t, err)
			defer tx.Rollback()
			items := []PassthroughLog{{ID: 11, RequestID: "one", UserID: 7}, {ID: 22, RequestID: "retry", UserID: 7}, {ID: 31, RequestID: "known", UserID: 7, Fallback: true, FallbackChecked: true}}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			markReadonlyFallbackRequests(ctx, tx, items)
			require.Equal(t, mode == "complete", items[0].FallbackChecked)
			require.False(t, items[0].Fallback)
			require.Equal(t, mode == "complete", items[1].FallbackChecked)
			require.Equal(t, mode == "complete", items[1].Fallback)
			if mode == "complete" {
				require.Equal(t, []string{"141", "148"}, items[1].FallbackChannels)
				require.Equal(t, 2, items[1].FallbackIndex)
				require.Equal(t, 2, items[1].FallbackTotal)
			}
			require.True(t, items[2].Fallback)
			require.True(t, items[2].FallbackChecked)
		})
	}
}
func TestReadonlySummaryGateLeavesConnectionForList(t *testing.T) {
	started, gate := make(chan struct{}, 2), make(chan struct{})
	db := sql.OpenDB(reviewConnector{func(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
		started <- struct{}{}
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &reviewRows{columns: []string{"count"}, values: [][]driver.Value{{int64(1)}}}, nil
	}})
	defer db.Close()
	configureReadonlyDB(db)
	h := &PassthroughHandler{Config: customerQueryConfig{}, pools: map[string]passthroughPool{"a": {encrypted: "test-pool", db: db}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			h.logCount(httptest.NewRecorder(), httptest.NewRequest("GET", reviewURL, nil).WithContext(ctx))
		}()
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first summary did not start")
	}
	listCtx, listCancel := context.WithTimeout(context.Background(), time.Second)
	defer listCancel()
	conn, err := db.Conn(listCtx)
	require.NoError(t, err, "summary work must leave the second connection available")
	require.Equal(t, 2, db.Stats().InUse)
	require.NoError(t, conn.Close())
	cancel()
	close(gate)
	<-done
	<-done
	// A cancelled waiter must not leak the permit or execute later.
	release, err := h.acquireReadonlySummary(context.Background(), db)
	require.NoError(t, err)
	release()
}
