package dashboard

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func waitReadonlyRawWaiters(t *testing.T, g *readonlyRawSummaryGroup, count int) {
	t.Helper()
	require.Eventually(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		for _, call := range g.calls {
			if call.waiters == count {
				return true
			}
		}
		return false
	}, time.Second, time.Millisecond)
}

func TestReadonlyRawSummarySharesCountAndQuotaButKeepsRateIndependent(t *testing.T) {
	for _, query := range []string{"&empty_output=1", "&status_code=429", "&fallback_final_only=1"} {
		t.Run(query, func(t *testing.T) {
			started, unblock := make(chan struct{}), make(chan struct{})
			var scans, rates atomic.Int32
			db := sql.OpenDB(reviewConnector{func(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
				if strings.Contains(q, "SUM(l.quota)") || strings.Contains(q, "CASE WHEN l.type=2") {
					if scans.Add(1) == 1 {
						close(started)
					}
					select {
					case <-unblock:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					return &reviewRows{columns: []string{"count", "quota"}, values: [][]driver.Value{{int64(9), int64(123)}}}, nil
				}
				rates.Add(1)
				if !strings.Contains(q, "SUM(l.prompt_tokens)") {
					return nil, errors.New("unexpected query: " + q)
				}
				if args[1].Value.(int64)-args[0].Value.(int64) != 60 {
					return nil, errors.New("rate window changed")
				}
				return &reviewRows{columns: []string{"rpm", "tpm"}, values: [][]driver.Value{{int64(3), int64(40)}}}, nil
			}})
			defer db.Close()
			h := &PassthroughHandler{Config: customerQueryConfig{}, pools: map[string]passthroughPool{"a": {encrypted: "test-pool", db: db}}}
			count, stat := httptest.NewRecorder(), httptest.NewRecorder()
			countDone, statDone := make(chan struct{}), make(chan struct{})
			go func() { defer close(countDone); h.LogCount(count, httptest.NewRequest("GET", reviewURL+query, nil)) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("summary did not start")
			}
			go func() { defer close(statDone); h.LogStat(stat, httptest.NewRequest("GET", reviewURL+query, nil)) }()
			waitReadonlyRawWaiters(t, &h.rawSummaries, 2)
			close(unblock)
			<-countDone
			<-statDone
			require.Equal(t, 200, count.Code, count.Body.String())
			require.Equal(t, 200, stat.Code, stat.Body.String())
			require.JSONEq(t, `{"configured":true,"total":9}`, count.Body.String())
			require.JSONEq(t, `{"configured":true,"summary":{"quota":123,"rpm":3,"tpm":40}}`, stat.Body.String())
			require.Equal(t, int32(1), scans.Load(), "two endpoints must share the expensive window scan")
			require.Equal(t, int32(1), rates.Load())
		})
	}
}

func TestReadonlyRawSummaryFailureDoesNotCoupleEndpointResults(t *testing.T) {
	for _, mode := range []string{"sum_failure", "rate_failure"} {
		t.Run(mode, func(t *testing.T) {
			db := sql.OpenDB(reviewConnector{func(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
				if strings.Contains(q, "SUM(l.prompt_tokens)") {
					if mode == "rate_failure" {
						return nil, errors.New("rate failed")
					}
					return &reviewRows{columns: []string{"rpm", "tpm"}, values: [][]driver.Value{{int64(3), int64(4)}}}, nil
				}
				if strings.Contains(q, "SUM(l.quota)") {
					if mode == "sum_failure" {
						return nil, errors.New("sum failed")
					}
					return &reviewRows{columns: []string{"count", "quota"}, values: [][]driver.Value{{int64(9), int64(12)}}}, nil
				}
				return &reviewRows{columns: []string{"count"}, values: [][]driver.Value{{int64(9)}}}, nil
			}})
			defer db.Close()
			h := &PassthroughHandler{Config: customerQueryConfig{}, pools: map[string]passthroughPool{"a": {encrypted: "test-pool", db: db}}}
			count, stat := httptest.NewRecorder(), httptest.NewRecorder()
			h.LogCount(count, httptest.NewRequest("GET", reviewURL+"&empty_output=1", nil))
			h.LogStat(stat, httptest.NewRequest("GET", reviewURL+"&empty_output=1", nil))
			require.Equal(t, 200, count.Code)
			require.JSONEq(t, `{"configured":true,"total":9}`, count.Body.String())
			require.Equal(t, 502, stat.Code)
		})
	}
}

func TestReadonlyRawSummaryCancellationAndNoExtraCache(t *testing.T) {
	var group readonlyRawSummaryGroup
	key := readonlyRawSummaryKey{}
	started, unblock := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	run := func(ctx context.Context) (readonlyRawSummary, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-unblock:
			return readonlyRawSummary{Count: 7}, nil
		case <-ctx.Done():
			return readonlyRawSummary{}, ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done1, done2 := make(chan error, 1), make(chan error, 1)
	go func() { _, err := group.do(ctx, key, run); done1 <- err }()
	<-started
	go func() { _, err := group.do(context.Background(), key, run); done2 <- err }()
	waitReadonlyRawWaiters(t, &group, 2)
	cancel()
	require.ErrorIs(t, <-done1, context.Canceled)
	close(unblock)
	require.NoError(t, <-done2)
	require.Equal(t, int32(1), calls.Load())
	_, err := group.do(context.Background(), key, run)
	require.NoError(t, err)
	require.Equal(t, int32(2), calls.Load(), "a later request must not reuse another endpoint's completed result")
}

func TestReadonlyRawSummaryAllWaitersCancelAndNextReadStartsFresh(t *testing.T) {
	var group readonlyRawSummaryGroup
	started, stopped := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := group.do(ctx, readonlyRawSummaryKey{}, func(work context.Context) (readonlyRawSummary, error) {
			close(started)
			<-work.Done()
			close(stopped)
			return readonlyRawSummary{}, work.Err()
		})
		done <- err
	}()
	<-started
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("orphaned raw summary query")
	}
	result, err := group.do(context.Background(), readonlyRawSummaryKey{}, func(context.Context) (readonlyRawSummary, error) {
		return readonlyRawSummary{Count: 8}, nil
	})
	require.NoError(t, err)
	require.Equal(t, int64(8), result.Count)
}

func TestReadonlyRawSummaryIdentitySeparatesScopeAndPool(t *testing.T) {
	started, unblock := make(chan struct{}, 8), make(chan struct{})
	var scans atomic.Int32
	connector := reviewConnector{func(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
		scans.Add(1)
		started <- struct{}{}
		select {
		case <-unblock:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &reviewRows{columns: []string{"count", "quota"}, values: [][]driver.Value{{int64(1), int64(2)}}}, nil
	}}
	db1, db2 := sql.OpenDB(connector), sql.OpenDB(connector)
	defer db1.Close()
	defer db2.Close()
	h := &PassthroughHandler{}
	done := make(chan error, 5)
	filters, err := parseReadonlyLogFilters(httptest.NewRequest("GET", "/?empty_output=1", nil).URL.Query(), []int64{7}, false)
	require.NoError(t, err)
	for _, input := range []struct {
		db     *sql.DB
		site   string
		viewer bool
		user   int64
	}{
		{db1, "a", false, 7}, {db2, "a", false, 7}, {db1, "b", false, 7}, {db1, "a", true, 7}, {db1, "a", false, 8},
	} {
		go func() {
			f := filters
			f.args = append([]any(nil), filters.args...)
			f.args[0] = input.user
			_, err := h.sharedReadonlyRawSummary(context.Background(), input.db, input.site, input.viewer, time.Unix(10, 0), time.Unix(20, 0), f)
			done <- err
		}()
	}
	// Workers on the same pool wait on its summary permit but remain distinct.
	require.Eventually(t, func() bool {
		h.rawSummaries.mu.Lock()
		defer h.rawSummaries.mu.Unlock()
		return len(h.rawSummaries.calls) == 5
	}, time.Second, time.Millisecond)
	close(unblock)
	for i := 0; i < 5; i++ {
		require.NoError(t, <-done)
	}
	require.Equal(t, int32(5), scans.Load())
}
