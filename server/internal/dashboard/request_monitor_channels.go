package dashboard

import (
	"context"
	"controltower/internal/latencyhist"
	"controltower/server/internal/aggregator"
	"controltower/server/internal/auth"
	"controltower/server/internal/storage"
	"net/http"
	"sort"
	"time"
)

type requestChannelSource interface {
	RequestMonitorMetrics(context.Context, time.Time, time.Time, ...string) ([]aggregator.Metric, error)
}
type ChannelMeasure struct {
	Value      *float64   `json:"value"`
	Samples    int64      `json:"samples"`
	Minutes    int        `json:"minutes"`
	LowerBound bool       `json:"lower_bound"`
	Trend      []*float64 `json:"trend"`
}
type SlowChannel struct {
	InstanceID   string         `json:"instance_id"`
	InstanceName string         `json:"instance_name"`
	Key          string         `json:"key"`
	Name         string         `json:"name"`
	Count        int64          `json:"count"`
	Latest       int64          `json:"latest"`
	TTFT         ChannelMeasure `json:"ttft"`
	Duration     ChannelMeasure `json:"duration"`
	Errors       ChannelMeasure `json:"errors"`
	Reasons      []string       `json:"reasons"`
	Unknown      []string       `json:"unknown"`
	Partial      bool           `json:"partial"`
	Score        float64        `json:"score"`
	// Retain existing response fields for clients being upgraded.
	Samples    int64      `json:"samples"`
	P95        float64    `json:"p95_seconds"`
	TailCapped bool       `json:"tail_capped"`
	Trend      []*float64 `json:"trend"`
}
type SlowChannels struct {
	From         int64                       `json:"from"`
	To           int64                       `json:"to"`
	QueriedAt    int64                       `json:"queried_at"`
	Latest       int64                       `json:"latest"`
	Excluded     int                         `json:"excluded"`
	Items        []SlowChannel               `json:"items"`
	Pending      []SlowChannel               `json:"pending"`
	PendingCount int                         `json:"pending_count"`
	Rules        storage.RequestMonitorRules `json:"rules"`
	Site         string                      `json:"site"`
}

func (h Handler) HandleRequestMonitorChannels(w http.ResponseWriter, r *http.Request) {
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
	source, ok := h.metricSource.(requestChannelSource)
	ruleStore, rulesOK := h.metricSource.(storage.RequestMonitorRulesStore)
	if !ok || !rulesOK {
		writeDashboardError(w, 503, "channel_metrics_unavailable")
		return
	}
	order := r.URL.Query().Get("sort")
	if order != "" && order != "volume" && order != "latency" {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rules, err := ruleStore.LoadRequestMonitorRules(ctx)
	if err != nil {
		writeDashboardError(w, 503, "channel_rules_unavailable")
		return
	}
	site := rules.SiteID
	if site == "" {
		writeDashboardError(w, 400, "channel_site_unconfigured")
		return
	}
	ids, err := h.instanceIDsForRequest("", site)
	if err != nil {
		writeDashboardError(w, 503, "channel_metrics_failed")
		return
	}
	now := time.Now().UTC()
	to := now.Truncate(time.Minute).Add(-time.Minute)
	from := to.Add(-time.Duration(rules.WindowMinutes) * time.Minute)
	var metrics []aggregator.Metric
	if len(ids) > 0 {
		metrics, err = source.RequestMonitorMetrics(ctx, from, to, ids...)
	}
	if err != nil {
		writeDashboardError(w, 503, "channel_metrics_failed")
		return
	}
	if len(metrics) > 10000 {
		writeDashboardError(w, 503, "channel_metrics_limit")
		return
	}
	// Scope before aggregation as well, so a source cannot leak unrelated rankings.
	allowed := instanceIDSet(ids)
	scoped := make([]aggregator.Metric, 0, len(metrics))
	for _, m := range metrics {
		if allowed[m.InstanceID] {
			scoped = append(scoped, m)
		}
	}
	result := buildSlowChannels(scoped, from, to, order, rules)
	result.QueriedAt = now.Unix()
	result.Site = site
	for _, items := range [][]SlowChannel{result.Items, result.Pending} {
		for i := range items {
			items[i].InstanceName = h.instanceName(items[i].InstanceID)
			items[i].Name = h.displayDimensionName("instance_channel", items[i].Key)
		}
	}
	writeDashboardJSON(w, 200, result)
}
func histogramValid(b []int64, n int64) bool {
	if n < 0 {
		return false
	}
	var sum int64
	for _, v := range b {
		if v < 0 || v > n-sum {
			return false
		}
		sum += v
	}
	return sum == n
}
func boundedP95(b []int64, bounds []float64, q *float64) (*float64, bool) {
	if q == nil {
		return nil, false
	}
	var total, before int64
	for i, v := range b {
		total += v
		if i < len(b)-1 {
			before += v
		}
	}
	if float64(before) < .95*float64(total) {
		v := bounds[len(bounds)-2]
		return &v, true
	}
	return q, false
}
func buildSlowChannels(metrics []aggregator.Metric, from, to time.Time, order string, configs ...storage.RequestMonitorRules) SlowChannels {
	rules := storage.DefaultRequestMonitorRules()
	if len(configs) > 0 {
		rules = configs[0]
	}
	size := int(to.Sub(from) / time.Minute)
	out := SlowChannels{From: from.Unix(), To: to.Unix(), Rules: rules, Items: []SlowChannel{}, Pending: []SlowChannel{}}
	type group struct {
		item           SlowChannel
		rows           map[int64][]aggregator.Metric
		ttft, duration latencyhist.BucketsV2
		legacy         latencyhist.Buckets
		usesLegacy     bool
		errors         int64
		ttftMissing    bool
	}
	groups := map[string]*group{}
	for _, m := range metrics {
		if m.DimensionType != "instance_channel" || m.BucketTime.Before(from) || !m.BucketTime.Before(to) {
			continue
		}
		key := m.InstanceID + "\x00" + m.DimensionKey
		g := groups[key]
		if g == nil {
			measure := func() ChannelMeasure { return ChannelMeasure{Trend: make([]*float64, size)} }
			g = &group{item: SlowChannel{InstanceID: m.InstanceID, Key: m.DimensionKey, TTFT: measure(), Duration: measure(), Errors: measure(), Reasons: []string{}, Unknown: []string{}}, rows: map[int64][]aggregator.Metric{}}
			groups[key] = g
		}
		at := m.BucketTime.Unix()
		g.rows[at] = append(g.rows[at], m)
	}
	for _, g := range groups {
		for at, rows := range g.rows {
			if len(rows) != 1 || at%60 != 0 || rows[0].RequestCount < 0 {
				g.item.Partial = true
				g.ttftMissing = true
				continue
			}
			m := rows[0]
			idx := int(m.BucketTime.Sub(from) / time.Minute)
			g.item.Count += m.RequestCount
			if at > g.item.Latest {
				g.item.Latest = at
			}
			if at > out.Latest {
				out.Latest = at
			}
			if m.TTFTCount != nil && *m.TTFTCount == 0 {
				// Non-stream requests have no TTFT samples; zero is not a missing record.
			} else if m.TTFTCount != nil && *m.TTFTCount > 0 && *m.TTFTCount <= m.RequestCount && m.TTFTBuckets != nil && histogramValid(m.TTFTBuckets[:], *m.TTFTCount) {
				g.ttft = latencyhist.AddV2(g.ttft, *m.TTFTBuckets)
				g.item.TTFT.Samples += *m.TTFTCount
				g.item.TTFT.Minutes++
				g.item.TTFT.Trend[idx], _ = boundedP95(m.TTFTBuckets[:], latencyhist.UpperBoundsV2[:], latencyhist.QuantileV2(*m.TTFTBuckets, .95))
			} else {
				g.item.Partial = true
				g.ttftMissing = true
			}
			if m.LatencyBucketsV2 != nil && histogramValid(m.LatencyBucketsV2[:], m.RequestCount) {
				g.duration = latencyhist.AddV2(g.duration, *m.LatencyBucketsV2)
				g.item.Duration.Samples += m.RequestCount
				g.item.Duration.Minutes++
				g.item.Duration.Trend[idx], _ = boundedP95(m.LatencyBucketsV2[:], latencyhist.UpperBoundsV2[:], latencyhist.QuantileV2(*m.LatencyBucketsV2, .95))
			} else if m.LatencyBucketsV2 == nil && histogramValid(m.LatencyBuckets[:], m.RequestCount) {
				g.usesLegacy = true
				g.legacy = latencyhist.Add(g.legacy, m.LatencyBuckets)
				g.item.Duration.Samples += m.RequestCount
				g.item.Duration.Minutes++
				g.item.Duration.Trend[idx], _ = boundedP95(m.LatencyBuckets[:], latencyhist.UpperBounds[:], latencyhist.Quantile(m.LatencyBuckets, .95))
			} else {
				g.item.Partial = true
			}
			if m.ErrorCount >= 0 && m.ErrorCount <= m.RequestCount {
				g.errors += m.ErrorCount
				g.item.Errors.Samples += m.RequestCount
				g.item.Errors.Minutes++
				if m.RequestCount > 0 {
					v := 100 * float64(m.ErrorCount) / float64(m.RequestCount)
					g.item.Errors.Trend[idx] = &v
				}
			} else {
				g.item.Partial = true
			}
		}
		g.item.TTFT.Value, g.item.TTFT.LowerBound = boundedP95(g.ttft[:], latencyhist.UpperBoundsV2[:], latencyhist.QuantileV2(g.ttft, .95))
		if g.usesLegacy {
			b := latencyhist.Add(g.legacy, latencyhist.DeriveV1(g.duration))
			g.item.Duration.Value, g.item.Duration.LowerBound = boundedP95(b[:], latencyhist.UpperBounds[:], latencyhist.Quantile(b, .95))
		} else {
			g.item.Duration.Value, g.item.Duration.LowerBound = boundedP95(g.duration[:], latencyhist.UpperBoundsV2[:], latencyhist.QuantileV2(g.duration, .95))
		}
		if g.item.Errors.Samples > 0 {
			v := 100 * float64(g.errors) / float64(g.item.Errors.Samples)
			g.item.Errors.Value = &v
		}
		if g.item.Count < rules.MinRequests {
			continue
		}
		consider := func(key string, m ChannelMeasure, threshold float64, min int64, applicable bool, incomplete bool) {
			if !applicable {
				return
			}
			if m.Value == nil || m.Samples < min {
				g.item.Unknown = append(g.item.Unknown, key)
				return
			}
			if *m.Value >= threshold {
				g.item.Reasons = append(g.item.Reasons, key)
				score := *m.Value / threshold
				if score > g.item.Score {
					g.item.Score = score
				}
			} else if m.LowerBound || incomplete {
				g.item.Unknown = append(g.item.Unknown, key)
			}
		}
		consider("ttft", g.item.TTFT, rules.TTFTSeconds, rules.MinSamples, g.ttftMissing || g.item.TTFT.Samples > 0, g.ttftMissing)
		consider("duration", g.item.Duration, rules.DurationSeconds, rules.MinSamples, true, g.item.Duration.Samples < g.item.Count)
		consider("error_rate", g.item.Errors, rules.ErrorPercent, rules.MinRequests, true, g.item.Errors.Samples < g.item.Count)
		g.item.Samples = g.item.TTFT.Samples
		g.item.Trend = g.item.TTFT.Trend
		g.item.TailCapped = g.item.TTFT.LowerBound
		if g.item.TTFT.Value != nil {
			g.item.P95 = *g.item.TTFT.Value
		}
		if len(g.item.Reasons) > 0 {
			out.Items = append(out.Items, g.item)
		} else if len(g.item.Unknown) > 0 {
			out.Pending = append(out.Pending, g.item)
		}
	}
	rank := func(items []SlowChannel, byScore bool) {
		sort.Slice(items, func(i, j int) bool {
			a, b := items[i], items[j]
			if byScore && a.Score != b.Score {
				return a.Score > b.Score
			}
			if a.Count != b.Count {
				return a.Count > b.Count
			}
			if a.Score != b.Score {
				return a.Score > b.Score
			}
			if a.InstanceID != b.InstanceID {
				return a.InstanceID < b.InstanceID
			}
			return a.Key < b.Key
		})
	}
	rank(out.Items, order == "latency")
	rank(out.Pending, false)
	out.PendingCount = len(out.Pending)
	out.Excluded = out.PendingCount
	if len(out.Items) > 5 {
		out.Items = out.Items[:5]
	}
	if len(out.Pending) > 10 {
		out.Pending = out.Pending[:10]
	}
	return out
}
