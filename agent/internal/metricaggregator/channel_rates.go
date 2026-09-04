package metricaggregator

import (
	"controltower/agent/internal/logcollector"
	"controltower/agent/internal/reporter"
	"strconv"
	"time"
)

// ChannelRates carries second-resolution counters in the existing atomic,
// retry-deduplicated metric batch. It does not perform any source queries.
func ChannelRates(events []logcollector.Event, now time.Time) []reporter.AggregatedMetricPayload {
	type key struct {
		channel int64
		second  int64
	}
	counts := map[key]reporter.AggregatedMetricPayload{}
	for _, event := range events {
		if event.ChannelID <= 0 || event.LogType != "consume" || event.CreatedAt.Before(now.Add(-2*time.Minute)) {
			continue
		}
		k := key{event.ChannelID, event.CreatedAt.Unix()}
		m := counts[k]
		m.BucketTime = time.Unix(k.second, 0).UTC()
		m.WindowSeconds = 1
		m.DimensionType = "channel_rate_second"
		m.DimensionKey = strconv.FormatInt(k.channel, 10)
		m.RequestCount++
		m.TPM += event.PromptTokens + event.CompletionTokens
		counts[k] = m
	}
	// Channel zero is a coverage marker, including polls with no new requests.
	out := []reporter.AggregatedMetricPayload{{BucketTime: now.UTC().Truncate(time.Second), WindowSeconds: 1, DimensionType: "channel_rate_second", DimensionKey: "0"}}
	for _, m := range counts {
		out = append(out, m)
	}
	return out
}
