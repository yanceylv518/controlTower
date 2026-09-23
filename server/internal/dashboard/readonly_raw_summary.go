package dashboard

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"sync"
	"time"
)

type readonlyRawSummary struct {
	Count int64
	Quota int64
}

type readonlyRawSummaryKey struct {
	db    *sql.DB
	scope [32]byte
}

type readonlyRawSummaryCall struct {
	done     chan struct{}
	cancel   context.CancelFunc
	waiters  int
	complete bool
	result   readonlyRawSummary
	err      error
}

// Only overlapping reads share work. Completed results are not retained here:
// the existing endpoint cache remains the sole five-second freshness boundary.
type readonlyRawSummaryGroup struct {
	mu    sync.Mutex
	calls map[readonlyRawSummaryKey]*readonlyRawSummaryCall
}

func (g *readonlyRawSummaryGroup) do(ctx context.Context, key readonlyRawSummaryKey, run func(context.Context) (readonlyRawSummary, error)) (readonlyRawSummary, error) {
	if err := ctx.Err(); err != nil {
		return readonlyRawSummary{}, err
	}
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[readonlyRawSummaryKey]*readonlyRawSummaryCall)
	}
	call := g.calls[key]
	if call == nil {
		if len(g.calls) >= 256 {
			g.mu.Unlock()
			return run(ctx)
		}
		work, cancel := context.WithTimeout(context.WithoutCancel(ctx), readonlyLogQueryTimeout)
		call = &readonlyRawSummaryCall{done: make(chan struct{}), cancel: cancel}
		g.calls[key] = call
		go func() {
			result, err := run(work)
			g.mu.Lock()
			call.result, call.err, call.complete = result, err, true
			if g.calls[key] == call {
				delete(g.calls, key)
			}
			close(call.done)
			g.mu.Unlock()
			cancel()
		}()
	}
	call.waiters++
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		call.waiters--
		if call.waiters == 0 && !call.complete {
			if g.calls[key] == call {
				delete(g.calls, key)
			}
			call.cancel()
		}
		g.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return readonlyRawSummary{}, ctx.Err()
	case <-call.done:
		return call.result, call.err
	}
}

func readonlyRawSummarySQL(start, end time.Time, filters readonlyLogFilters) (string, []any) {
	quota := "l.quota"
	if filters.logType == nil {
		// Count includes all matching types; default usage still includes only
		// consumption. An explicit type retains the existing SUM semantics.
		quota = "CASE WHEN l.type=2 THEN l.quota ELSE 0 END"
	}
	query := "SELECT COUNT(*),COALESCE(SUM(" + quota + "),0) FROM logs l WHERE l.created_at>=? AND l.created_at<?" + filters.where
	return query, append([]any{start.Unix(), end.Unix()}, filters.args...)
}

func (h *PassthroughHandler) sharedReadonlyRawSummary(ctx context.Context, db *sql.DB, site string, viewer bool, start, end time.Time, filters readonlyLogFilters) (readonlyRawSummary, error) {
	query, args := readonlyRawSummarySQL(start, end, filters)
	identity, _ := json.Marshal([]any{site, viewer, query, args})
	key := readonlyRawSummaryKey{db: db, scope: sha256.Sum256(identity)}
	return h.rawSummaries.do(ctx, key, func(work context.Context) (readonlyRawSummary, error) {
		// Acquire the permit inside the shared worker so a second endpoint can
		// join while the first endpoint is querying or waiting for a connection.
		release, err := h.acquireReadonlySummary(work, db)
		if err != nil {
			return readonlyRawSummary{}, err
		}
		defer release()
		var result readonlyRawSummary
		err = db.QueryRowContext(work, query, args...).Scan(&result.Count, &result.Quota)
		if err != nil {
			logReadonlyQueryFailure(site, "count_stat", "shared_query", err)
		}
		return result, err
	})
}
