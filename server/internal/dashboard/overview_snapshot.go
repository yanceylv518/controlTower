package dashboard

import (
	"context"
	"controltower/server/internal/aggregator"
	"controltower/server/internal/storage"
	"net/http"
	"time"
)

type overviewSnapshotStore interface {
	OverviewCoverage(context.Context, string, time.Time, time.Time) ([]string, []storage.OverviewCoverage, error)
	OverviewMetrics(context.Context, []string, time.Time, time.Time) ([]aggregator.Metric, error)
	OverviewRuntime(context.Context, string) ([]storage.OverviewInstance, error)
	OverviewAlerts(context.Context, string, time.Time) (storage.OverviewAlertSnapshot, error)
}
type OverviewPoint struct {
	From        time.Time      `json:"from"`
	To          time.Time      `json:"to"`
	Complete    bool           `json:"complete"`
	Received    int            `json:"received_sources"`
	ConfirmedAt *time.Time     `json:"confirmed_at"`
	Metrics     *MetricSummary `json:"metrics"`
}
type OverviewRequests struct {
	Site        string          `json:"site"`
	QueriedAt   time.Time       `json:"queried_at"`
	Sources     []string        `json:"sources"`
	Recent      OverviewPoint   `json:"recent"`
	Points      []OverviewPoint `json:"points"`
	StepSeconds int             `json:"step_seconds"`
}

func buildOverviewPoint(from, to time.Time, ids []string, metrics []aggregator.Metric, coverage []storage.OverviewCoverage) OverviewPoint {
	p := OverviewPoint{From: from, To: to}
	expected := map[string]bool{}
	for _, id := range ids {
		expected[id] = true
	}
	seen := map[string]bool{}
	confirmed := map[string]bool{}
	for _, c := range coverage {
		if expected[c.InstanceID] && !c.BucketTime.Before(from) && c.BucketTime.Before(to) {
			confirmed[c.InstanceID+"/"+c.BucketTime.UTC().Format(time.RFC3339)] = true
			if p.ConfirmedAt == nil || c.ConfirmedAt.After(*p.ConfirmedAt) {
				at := c.ConfirmedAt
				p.ConfirmedAt = &at
			}
		}
	}
	normalized := []aggregator.Metric{}
	dedup := map[string]bool{}
	for _, m := range metrics {
		if !expected[m.InstanceID] || m.DimensionType != "instance" || m.BucketTime.Before(from) || !m.BucketTime.Before(to) {
			continue
		}
		key := m.InstanceID + "/" + m.BucketTime.UTC().Format(time.RFC3339)
		if dedup[key] {
			continue
		}
		dedup[key] = true
		seen[m.InstanceID] = true
		m.BucketTime = from
		normalized = append(normalized, m)
	}
	p.Complete = len(ids) > 0
	for _, id := range ids {
		full := true
		for at := from; at.Before(to); at = at.Add(time.Minute) {
			if !confirmed[id+"/"+at.UTC().Format(time.RFC3339)] {
				full = false
				p.Complete = false
			}
		}
		if full {
			seen[id] = true
		}
	}
	p.Received = len(seen)
	if len(normalized) > 0 || p.Complete {
		summary := BuildOverview(normalized).Recent1m
		p.Metrics = &summary
		if !p.Complete {
			summary.SuccessRate = nil
			summary.ErrorRate = nil
			summary.AvgUseTime = nil
			summary.P95UseTime = nil
			summary.P95LowerBound = false
		}
	}
	if !p.Complete {
		p.ConfirmedAt = nil
	}
	return p
}

func (h Handler) HandleOverviewSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	site := r.URL.Query().Get("site")
	if site == "" {
		writeDashboardError(w, 400, "site_required")
		return
	}
	store, ok := h.source.(overviewSnapshotStore)
	if !ok {
		writeDashboardError(w, 503, "overview_snapshot_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	now := time.Now().UTC()
	switch r.URL.Query().Get("section") {
	case "requests":
		hours := parseInt(r.URL.Query().Get("hours"))
		if hours != 1 && hours != 6 && hours != 24 {
			writeDashboardError(w, 400, "invalid_hours")
			return
		}
		end := now.Truncate(time.Minute)
		start := end.Add(-time.Duration(hours) * time.Hour)
		ids, coverage, err := store.OverviewCoverage(ctx, site, start, end)
		if err != nil {
			writeDashboardError(w, 503, "overview_requests_unavailable")
			return
		}
		metrics, err := store.OverviewMetrics(ctx, ids, start, end)
		if err != nil {
			writeDashboardError(w, 503, "overview_requests_unavailable")
			return
		}
		step := time.Minute
		if hours == 24 {
			step = 5 * time.Minute
		}
		result := OverviewRequests{Site: site, QueriedAt: now, Sources: ids, StepSeconds: int(step.Seconds()), Points: []OverviewPoint{}}
		result.Recent = buildOverviewPoint(end.Add(-time.Minute), end, ids, metrics, coverage)
		// Index by minute to avoid scanning 24h of source rows for every point.
		mm := map[int64][]aggregator.Metric{}
		cc := map[int64][]storage.OverviewCoverage{}
		for _, m := range metrics {
			key := m.BucketTime.Unix()
			mm[key] = append(mm[key], m)
		}
		for _, c := range coverage {
			key := c.BucketTime.Unix()
			cc[key] = append(cc[key], c)
		}
		for at := start; at.Before(end); at = at.Add(step) {
			var ms []aggregator.Metric
			var cs []storage.OverviewCoverage
			for minute := at; minute.Before(at.Add(step)); minute = minute.Add(time.Minute) {
				ms = append(ms, mm[minute.Unix()]...)
				cs = append(cs, cc[minute.Unix()]...)
			}
			result.Points = append(result.Points, buildOverviewPoint(at, at.Add(step), ids, ms, cs))
		}
		writeDashboardJSON(w, 200, result)
	case "runtime":
		items, err := store.OverviewRuntime(ctx, site)
		if err != nil {
			writeDashboardError(w, 503, "overview_runtime_unavailable")
			return
		}
		offlineSeconds := 120
		if h.settings != nil {
			if v, e := h.settings.Current(); e == nil && v.OfflineSeconds > 0 {
				offlineSeconds = v.OfflineSeconds
			}
		}
		type row struct {
			storage.OverviewInstance
			Online bool                  `json:"online"`
			Health []HealthCheckSummary  `json:"health"`
			Docker []DockerStatusSummary `json:"docker"`
		}
		out := []row{}
		for _, item := range items {
			online := item.Enabled && item.LastSeenAt != nil && freshOverviewTime(*item.LastSeenAt, now, time.Duration(offlineSeconds)*time.Second)
			out = append(out, row{item, online, summarizeHealthChecks(item.Health), summarizeDockerStatuses(item.Docker)})
		}
		writeDashboardJSON(w, 200, map[string]any{"site": site, "queried_at": now, "stale_after_seconds": offlineSeconds, "items": out})
	case "alerts":
		snapshot, err := store.OverviewAlerts(ctx, site, now)
		if err != nil {
			writeDashboardError(w, 503, "overview_alerts_unavailable")
			return
		}
		fresh := snapshot.EvaluatedAt != nil && freshOverviewTime(*snapshot.EvaluatedAt, now, 2*time.Minute)
		type item struct {
			AlertItem
			ConfirmedAt *time.Time `json:"confirmed_at"`
		}
		items := []item{}
		if fresh {
			for _, a := range snapshot.Items {
				v := storageAlertsToItems([]storage.Alert{a})[0]
				v.InstanceName = h.instanceName(a.InstanceID)
				items = append(items, item{v, a.ConfirmedAt})
			}
		} else {
			snapshot.Unconfirmed = snapshot.Total
		}
		writeDashboardJSON(w, 200, map[string]any{"site": site, "queried_at": now, "evaluated_at": snapshot.EvaluatedAt, "fresh": fresh, "items": items, "total": snapshot.Total - snapshot.Unconfirmed, "unconfirmed": snapshot.Unconfirmed})
	default:
		writeDashboardError(w, 400, "invalid_section")
	}
}
func freshOverviewTime(at, now time.Time, ttl time.Duration) bool {
	return !at.IsZero() && !at.After(now.Add(5*time.Second)) && now.Sub(at) <= ttl
}
