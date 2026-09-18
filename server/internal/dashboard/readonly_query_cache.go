package dashboard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// A bounded, short-lived cache also coalesces identical in-flight reads. Scope
// and encrypted connection identity are part of the key; errors are never cached.
type readonlyQueryCache struct {
	mu      sync.Mutex
	entries map[[32]byte]*readonlyCachedQuery
}
type readonlyCachedQuery struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	expires time.Time
	result  *readonlyResponseBuffer
}
type readonlyResponseBuffer struct {
	bytes.Buffer
	header http.Header
	status int
}

func (b *readonlyResponseBuffer) Header() http.Header    { return b.header }
func (b *readonlyResponseBuffer) WriteHeader(status int) { b.status = status }

func (h *PassthroughHandler) LogStat(w http.ResponseWriter, r *http.Request) {
	h.cachedReadonlySummary(w, r, "stat", h.logStat)
}
func (h *PassthroughHandler) LogCount(w http.ResponseWriter, r *http.Request) {
	h.cachedReadonlySummary(w, r, "count", h.logCount)
}
func (h *PassthroughHandler) cachedReadonlySummary(w http.ResponseWriter, r *http.Request, kind string, handler http.HandlerFunc) {
	site, ids, scopeErr := passthroughScope(r)
	run := func(ctx context.Context) *readonlyResponseBuffer {
		b := &readonlyResponseBuffer{header: make(http.Header), status: 200}
		handler(b, r.Clone(ctx))
		return b
	}
	var result *readonlyResponseBuffer
	var dsn string
	if scopeErr == nil && h.Config != nil {
		dsn, _ = h.Config.ReadonlyDSNForSite(site)
	}
	q := r.URL.Query()
	cacheable := scopeErr == nil && dsn != "" && firstQueryValue(q, "start_time", "start_timestamp") != "" && firstQueryValue(q, "end_time", "end_timestamp") != ""
	if cacheable {
		for _, field := range []string{"limit", "offset", "p", "page_size", "cursor"} {
			q.Del(field)
		}
		identity, _ := json.Marshal([]any{kind, site, ids, readonlyViewer(r), dsn, q.Encode()})
		key := sha256.Sum256(identity)
		h.mu.Lock()
		if h.summaryCache == nil {
			h.summaryCache = &readonlyQueryCache{entries: make(map[[32]byte]*readonlyCachedQuery)}
		}
		cache := h.summaryCache
		h.mu.Unlock()
		result = cache.get(r.Context(), key, run)
	} else {
		result = run(r.Context())
	}
	// Audit each caller once, including cache hits and coalesced callers; the
	// worker has no audit side effects. Cancellation is never logged as success.
	if scopeErr == nil {
		status, code := "failed", 499
		if result != nil && r.Context().Err() == nil {
			code = result.status
			if code >= 200 && code < 300 {
				status = "succeeded"
			}
		}
		summary := map[string]any{"user_ids": ids, "http_status": code, "start_time": firstQueryValue(q, "start_time", "start_timestamp"), "end_time": firstQueryValue(q, "end_time", "end_timestamp")}
		if result != nil && status == "succeeded" {
			var value struct {
				Total *int64 `json:"total"`
			}
			if json.Unmarshal(result.Bytes(), &value) == nil && value.Total != nil {
				summary["total"] = *value.Total
			}
		}
		h.auditStatus(r, site, "passthrough.logs."+kind, summary, status)
	}
	if result == nil || r.Context().Err() != nil {
		return
	}
	for key, values := range result.header {
		w.Header()[key] = append([]string(nil), values...)
	}
	w.Header().Set("Cache-Control", "no-store")
	if cacheable {
		w.Header().Set("X-CT-Statistics-Max-Age", "5")
	}
	w.WriteHeader(result.status)
	_, _ = w.Write(result.Bytes())
}
func (c *readonlyQueryCache) get(ctx context.Context, key [32]byte, run func(context.Context) *readonlyResponseBuffer) *readonlyResponseBuffer {
	c.mu.Lock()
	now := time.Now()
	for k, e := range c.entries {
		if e.result != nil && !now.Before(e.expires) {
			delete(c.entries, k)
		}
	}
	e := c.entries[key]
	if e == nil {
		if len(c.entries) >= 256 {
			c.mu.Unlock()
			return run(ctx)
		}
		work, cancel := context.WithTimeout(context.WithoutCancel(ctx), readonlyLogQueryTimeout)
		e = &readonlyCachedQuery{done: make(chan struct{}), cancel: cancel}
		c.entries[key] = e
		go func() {
			result := run(work)
			c.mu.Lock()
			e.result = result
			e.expires = time.Now().Add(5 * time.Second)
			if (result.status != 200 || work.Err() != nil) && c.entries[key] == e {
				delete(c.entries, key)
			}
			close(e.done)
			c.mu.Unlock()
			cancel()
		}()
	}
	e.waiters++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		e.waiters--
		if e.waiters == 0 && e.result == nil {
			if c.entries[key] == e {
				delete(c.entries, key)
			}
			e.cancel()
		}
		c.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return nil
	case <-e.done:
		return e.result
	}
}
