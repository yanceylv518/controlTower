package dashboard

import (
	"context"
	"database/sql"
)

// Stats and count share at most one of a site's two readonly connections.
// The permit spans all queries in a summary, including its rollup lookup, so
// multiple users and cache misses cannot fill the pool with summary work.
func (h *PassthroughHandler) acquireReadonlySummary(ctx context.Context, db *sql.DB) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h.mu.Lock()
	if h.summarySlots == nil {
		h.summarySlots = make(map[*sql.DB]chan struct{})
	}
	slot := h.summarySlots[db]
	if slot == nil {
		slot = make(chan struct{}, 1)
		h.summarySlots[db] = slot
	}
	h.mu.Unlock()
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
