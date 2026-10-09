package dashboard

import (
	"context"
	"controltower/server/internal/alblog"
	"controltower/server/internal/auth"
	"errors"
	"net/http"
	"sync"
	"time"
)

// One bounded cache per server process, shared by every viewer and Host filter.
// Failed results are cached too, to avoid hammering SLS during an outage.
type RequestMonitorHandler struct {
	Store   alblog.Store
	Client  alblog.Client
	Query   func(context.Context, alblog.Config) alblog.Monitor
	mu      sync.Mutex
	config  alblog.Config
	cached  alblog.Monitor
	expires time.Time
	running chan struct{}
}

func (h *RequestMonitorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	u, ok := auth.CurrentUser(r)
	if !ok || !auth.HasPermission(u, "monitor.requests") {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	if r.Method != http.MethodGet {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	cfg, err := h.Store.LoadALBLogConfig(r.Context())
	if errors.Is(err, alblog.ErrMissing) {
		writeDashboardJSON(w, 200, alblog.Monitor{Status: "unconfigured", Rows: []alblog.Minute{}})
		return
	}
	if err != nil {
		writeDashboardError(w, 503, "alb_config_unavailable")
		return
	}
	for {
		h.mu.Lock()
		if alblog.SameConnection(cfg, h.config) && time.Now().Before(h.expires) {
			data := h.cached
			h.mu.Unlock()
			writeDashboardJSON(w, 200, data)
			return
		}
		if pending := h.running; pending != nil {
			h.mu.Unlock()
			select {
			case <-pending:
				continue
			case <-r.Context().Done():
				return
			}
		}
		h.running = make(chan struct{})
		pending := h.running
		h.mu.Unlock()
		query := h.Query
		if query == nil {
			query = h.Client.Monitor
		}
		data := query(context.WithoutCancel(r.Context()), cfg)
		h.mu.Lock()
		h.cached = data
		h.config = cfg
		h.expires = time.Now().Add(30 * time.Second)
		h.running = nil
		close(pending)
		h.mu.Unlock()
		writeDashboardJSON(w, 200, data)
		return
	}
}
