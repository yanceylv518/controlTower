package dashboard

import (
	"context"
	"controltower/internal/latencyhist"
	"controltower/server/internal/aggregator"
	"controltower/server/internal/auth"
	"math"
	"net/http"
	"sort"
	"time"
)

type requestChannelSource interface {
	RequestMonitorMetrics(context.Context, time.Time, time.Time) ([]aggregator.Metric, error)
}
type SlowChannel struct {
	InstanceID   string     `json:"instance_id"`
	InstanceName string     `json:"instance_name"`
	Key          string     `json:"key"`
	Name         string     `json:"name"`
	Count        int64      `json:"count"`
	Samples      int64      `json:"samples"`
	P95          float64    `json:"p95_seconds"`
	TailCapped   bool       `json:"tail_capped"`
	Latest       int64      `json:"latest"`
	Trend        []*float64 `json:"trend"`
}
type SlowChannels struct {
	From      int64         `json:"from"`
	To        int64         `json:"to"`
	QueriedAt int64         `json:"queried_at"`
	Latest    int64         `json:"latest"`
	Excluded  int           `json:"excluded"`
	Items     []SlowChannel `json:"items"`
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
	if !ok {
		writeDashboardError(w, 503, "channel_metrics_unavailable")
		return
	}
	order := r.URL.Query().Get("sort")
	if order != "" && order != "volume" && order != "latency" {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	now := time.Now().UTC()
	to := now.Truncate(time.Minute).Add(-time.Minute)
	from := to.Add(-5 * time.Minute)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	metrics, err := source.RequestMonitorMetrics(ctx, from, to)
	if err != nil {
		writeDashboardError(w, 503, "channel_metrics_failed")
		return
	}
	if len(metrics) > 10000 {
		writeDashboardError(w, 503, "channel_metrics_limit")
		return
	}
	result := buildSlowChannels(metrics, from, to, order)
	result.QueriedAt = now.Unix()
	for i := range result.Items {
		item := &result.Items[i]
		item.InstanceName = h.instanceName(item.InstanceID)
		item.Name = h.displayDimensionName("instance_channel", item.Key)
	}
	writeDashboardJSON(w, 200, result)
}

func buildSlowChannels(metrics []aggregator.Metric, from, to time.Time, order string) SlowChannels {
	out := SlowChannels{From: from.Unix(), To: to.Unix(), Items: []SlowChannel{}}
	type group struct {
		item    SlowChannel
		hist    latencyhist.BucketsV2
		invalid bool
		seen    map[int64]bool
	}
	groups := map[string]*group{}
	for _, m := range metrics {
		if m.DimensionType != "instance_channel" || m.BucketTime.Before(from) || !m.BucketTime.Before(to) {
			continue
		}
		key := m.InstanceID + "\x00" + m.DimensionKey
		g := groups[key]
		if g == nil {
			g = &group{item: SlowChannel{InstanceID: m.InstanceID, Key: m.DimensionKey, Trend: make([]*float64, 5)}, seen: map[int64]bool{}}
			groups[key] = g
		}
		timestamp := m.BucketTime.Unix()
		if timestamp > out.Latest {
			out.Latest = timestamp
		}
		if g.seen[timestamp] || timestamp%60 != 0 || m.RequestCount < 0 {
			g.invalid = true
			continue
		}
		g.seen[timestamp] = true
		g.item.Count += m.RequestCount
		if timestamp > g.item.Latest {
			g.item.Latest = timestamp
		}
		if m.TTFTCount == nil || *m.TTFTCount < 0 || *m.TTFTCount > m.RequestCount {
			g.invalid = true
			continue
		}
		if *m.TTFTCount == 0 {
			continue
		}
		if m.TTFTBuckets == nil {
			g.invalid = true
			continue
		}
		var n int64
		for _, v := range *m.TTFTBuckets {
			if v < 0 {
				g.invalid = true
			}
			n += v
		}
		if n != *m.TTFTCount {
			g.invalid = true
			continue
		}
		g.item.Samples += n
		g.hist = latencyhist.AddV2(g.hist, *m.TTFTBuckets)
		index := int(m.BucketTime.Sub(from) / time.Minute)
		g.item.Trend[index] = latencyhist.QuantileV2(*m.TTFTBuckets, .95)
	}
	for _, g := range groups {
		if g.invalid {
			out.Excluded++
			continue
		}
		if g.item.Count < 100 || g.item.Samples < 20 {
			continue
		}
		p95 := latencyhist.QuantileV2(g.hist, .95)
		if p95 == nil || math.IsNaN(*p95) || *p95 < 10 {
			continue
		}
		g.item.P95 = *p95
		var beforeTail int64
		for _, v := range g.hist[:len(g.hist)-1] {
			beforeTail += v
		}
		g.item.TailCapped = float64(beforeTail) < .95*float64(g.item.Samples)
		out.Items = append(out.Items, g.item)
	}
	sort.Slice(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if order == "latency" && a.P95 != b.P95 {
			return a.P95 > b.P95
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if a.P95 != b.P95 {
			return a.P95 > b.P95
		}
		if a.InstanceID != b.InstanceID {
			return a.InstanceID < b.InstanceID
		}
		return a.Key < b.Key
	})
	if len(out.Items) > 5 {
		out.Items = out.Items[:5]
	}
	return out
}
