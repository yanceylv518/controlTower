package aggregator

import "time"

// Old buckets are acknowledged without replaying expired deduplication keys.
// The extra day protects boundaries and rollups. Expiry also follows future
// bucket timestamps, so an accepted clock-skewed batch cannot be replayed early.
const MetricBatchReplayWindow = 30 * 24 * time.Hour
const MetricBatchRetention = 31 * 24 * time.Hour

func RetainMetricBatch(metrics []Metric, now time.Time) ([]Metric, time.Time) {
	cutoff := now.Add(-MetricBatchReplayWindow)
	latest := now
	kept := make([]Metric, 0, len(metrics))
	for _, metric := range metrics {
		if metric.BucketTime.Before(cutoff) {
			continue
		}
		kept = append(kept, metric)
		if metric.BucketTime.After(latest) {
			latest = metric.BucketTime
		}
	}
	return kept, latest.Add(MetricBatchRetention)
}
