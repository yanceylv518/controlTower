package metricaggregator

import (
	"controltower/agent/internal/logcollector"
	"controltower/agent/internal/reporter"
	"strconv"
	"time"
)

// UserRates reuses collected logs; user zero independently certifies support
// for customer rate reporting (an older channel-only Agent cannot certify it).
func UserRates(events []logcollector.Event, now time.Time) []reporter.AggregatedMetricPayload {
	type key struct{ user, second int64 }
	counts := map[key]reporter.AggregatedMetricPayload{}
	for _, e := range events {
		if e.UserID <= 0 || e.LogType != "consume" || e.CreatedAt.Before(now.Add(-10*time.Minute)) || e.CreatedAt.After(now) {
			continue
		}
		k := key{e.UserID, e.CreatedAt.Unix()}
		m := counts[k]
		m.BucketTime = time.Unix(k.second, 0).UTC()
		m.WindowSeconds = 1
		m.DimensionType = "user_rate_second"
		m.DimensionKey = strconv.FormatInt(k.user, 10)
		m.RequestCount++
		m.TPM += e.PromptTokens + e.CompletionTokens
		counts[k] = m
	}
	out := []reporter.AggregatedMetricPayload{{BucketTime: now.UTC().Truncate(time.Second), WindowSeconds: 1, DimensionType: "user_rate_second", DimensionKey: "0"}}
	for _, m := range counts {
		out = append(out, m)
	}
	return out
}
