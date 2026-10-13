package dashboard

import (
	"context"
	"controltower/server/internal/alblog"
	"controltower/server/internal/auth"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// One bounded cache per server process, shared by every viewer and Host filter.
// Failed results are cached too, to avoid hammering SLS during an outage.
type RequestMonitorHandler struct {
	Store   alblog.Store
	Client  alblog.Client
	Query   func(context.Context, alblog.Config, alblog.MonitorRange) alblog.Monitor
	mu      sync.Mutex
	config  alblog.Config
	window  alblog.MonitorRange
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
	window, valid := requestMonitorRange(r)
	if !valid {
		writeDashboardError(w, 400, "alb_invalid_range")
		return
	}
	cfg, err := h.Store.LoadALBLogConfig(r.Context())
	if errors.Is(err, alblog.ErrMissing) {
		writeDashboardJSON(w, 200, alblog.Monitor{RangeSupported: true, Status: "unconfigured", Rows: []alblog.Minute{}})
		return
	}
	if err != nil {
		writeDashboardError(w, 503, "alb_config_unavailable")
		return
	}
	for {
		h.mu.Lock()
		if window == h.window && alblog.SameConnection(cfg, h.config) && time.Now().Before(h.expires) {
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
			query = h.Client.MonitorRange
		}
		data := query(context.WithoutCancel(r.Context()), cfg, window)
		h.mu.Lock()
		h.cached = data
		h.config = cfg
		h.window = window
		h.expires = time.Now().Add(30 * time.Second)
		h.running = nil
		close(pending)
		h.mu.Unlock()
		writeDashboardJSON(w, 200, data)
		return
	}
}

func requestMonitorRange(r *http.Request) (alblog.MonitorRange, bool) {
	q := r.URL.Query()
	window := alblog.MonitorRange{Minutes: 60}
	if q.Has("from") || q.Has("to") {
		if q.Has("minutes") || len(q["from"]) != 1 || len(q["to"]) != 1 {
			return window, false
		}
		from, err1 := strconv.ParseInt(q.Get("from"), 10, 64)
		to, err2 := strconv.ParseInt(q.Get("to"), 10, 64)
		window = alblog.MonitorRange{From: from, To: to}
		return window, err1 == nil && err2 == nil && from > 0 && to > 0 && window.Valid(time.Now())
	}
	if q.Has("minutes") {
		if len(q["minutes"]) != 1 {
			return window, false
		}
		n, err := strconv.Atoi(q.Get("minutes"))
		if err != nil || n < 1 {
			return window, false
		}
		window.Minutes = n
	}
	return window, window.Valid(time.Now())
}
