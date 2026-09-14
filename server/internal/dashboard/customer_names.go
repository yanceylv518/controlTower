package dashboard

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// CustomerNameSource reads account metadata, independently of log sampling.
type CustomerNameSource interface {
	CustomerNames(context.Context, string) (map[int64]string, error)
}

// CustomerNames intentionally reads only identity fields, never balances or
// credentials. The existing site-specific readonly connection is reused.
func (h *PassthroughHandler) CustomerNames(ctx context.Context, site string) (map[int64]string, error) {
	db, configured, err := h.database(site)
	if err != nil {
		return nil, err
	}
	if !configured {
		return nil, fmt.Errorf("customer directory unavailable")
	}
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, `SELECT id,COALESCE(username,''),COALESCE(display_name,'') FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make(map[int64]string)
	for rows.Next() {
		var id int64
		var username, displayName string
		if err := rows.Scan(&id, &username, &displayName); err != nil {
			return nil, err
		}
		name := strings.TrimSpace(username)
		if name == "" {
			name = strings.TrimSpace(displayName)
		}
		if id > 0 && name != "" {
			items[id] = name
		}
	}
	return items, rows.Err()
}

type customerNameSnapshot struct {
	mu          sync.Mutex
	names       map[int64]string
	nextRefresh time.Time
}

type customerNameDirectory struct {
	source CustomerNameSource
	mu     sync.Mutex
	sites  map[string]*customerNameSnapshot
}

func (h Handler) WithCustomerNameSource(source CustomerNameSource) Handler {
	if h.names != nil && source != nil {
		h.names.customers = &customerNameDirectory{source: source, sites: make(map[string]*customerNameSnapshot)}
	}
	return h
}

func (d *customerNameDirectory) name(site string, id int64, now time.Time) string {
	d.mu.Lock()
	snapshot := d.sites[site]
	if snapshot == nil {
		snapshot = &customerNameSnapshot{}
		d.sites[site] = snapshot
	}
	d.mu.Unlock()
	// Coalesce concurrent requests for one site without holding the global
	// name resolver lock or blocking other sites/channel names on source I/O.
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	if !now.Before(snapshot.nextRefresh) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		names, err := d.source.CustomerNames(ctx, site)
		cancel()
		if err == nil {
			snapshot.names = names
			snapshot.nextRefresh = now.Add(5 * time.Minute)
		} else {
			// Preserve the last successful snapshot; also negative-cache cold
			// failures so every card cannot start another failing source query.
			snapshot.nextRefresh = now.Add(time.Minute)
		}
	}
	return snapshot.names[id]
}

func (r *nameResolver) customerName(instanceID string, userID int64) string {
	if r == nil || r.source == nil || r.customers == nil || userID <= 0 {
		return ""
	}
	site := r.resolve("customer-site:"+instanceID, "", func() (string, error) {
		instance, ok, err := r.source.InstanceByID(instanceID)
		if err != nil || !ok {
			return "", err
		}
		return siteOf(instance), nil
	})
	if site == "" {
		return ""
	}
	return r.customers.name(site, userID, r.now())
}
