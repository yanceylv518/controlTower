package aggregator

import (
	"sort"
	"time"

	"controltower/internal/latencyhist"
)

type rollupAccumulator struct {
	metric Metric
}

func Rollup5m(metrics []Metric) []Metric {
	accumulators := make(map[string]*rollupAccumulator)
	for _, metric := range metrics {
		bucket := truncateTo5m(metric.BucketTime)
		key := metric.InstanceID + "|" + bucket.Format(time.RFC3339) + "|" + metric.DimensionType + "|" + metric.DimensionKey
		acc := accumulators[key]
		if acc == nil {
			acc = &rollupAccumulator{
				metric: Metric{
					InstanceID:    metric.InstanceID,
					BucketTime:    bucket,
					DimensionType: metric.DimensionType,
					DimensionKey:  metric.DimensionKey,
				},
			}
			accumulators[key] = acc
		}
		acc.add(metric)
	}

	results := make([]Metric, 0, len(accumulators))
	for _, acc := range accumulators {
		results = append(results, acc.finalize())
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].BucketTime.Equal(results[j].BucketTime) {
			if results[i].DimensionType == results[j].DimensionType {
				return results[i].DimensionKey < results[j].DimensionKey
			}
			return results[i].DimensionType < results[j].DimensionType
		}
		return results[i].BucketTime.Before(results[j].BucketTime)
	})
	return results
}

func (a *rollupAccumulator) add(metric Metric) {
	a.metric.RequestCount += metric.RequestCount
	a.metric.SuccessCount += metric.SuccessCount
	a.metric.ErrorCount += metric.ErrorCount
	a.metric.TPM += metric.TPM
	a.metric.PromptTokens += metric.PromptTokens
	a.metric.CompletionTokens += metric.CompletionTokens
	a.metric.Quota += metric.Quota
	if metric.UseTimeSum > 0 {
		a.metric.UseTimeSum += metric.UseTimeSum
	} else if metric.AvgUseTime != nil {
		a.metric.UseTimeSum += *metric.AvgUseTime * float64(metric.RequestCount)
	}
	if metric.StreamCount > 0 {
		a.metric.StreamCount += metric.StreamCount
	} else if metric.StreamRate != nil {
		a.metric.StreamCount += int64(*metric.StreamRate * float64(metric.RequestCount))
	}
	a.metric.CacheTokensTotal += metric.CacheTokensTotal
	a.metric.CachePromptTokens += metric.CachePromptTokens
	a.metric.LatencyBuckets = latencyhist.Add(a.metric.LatencyBuckets, metric.LatencyBuckets)

}

func (a *rollupAccumulator) finalize() Metric {
	metric := a.metric
	metric.SuccessRate = ratio(metric.SuccessCount, metric.RequestCount)
	metric.ErrorRate = ratio(metric.ErrorCount, metric.RequestCount)
	metric.StreamRate = ratio(metric.StreamCount, metric.RequestCount)
	if metric.RequestCount > 0 {
		metric.AvgUseTime = floatPtr(metric.UseTimeSum / float64(metric.RequestCount))
	}
	metric.P95UseTime = latencyhist.Quantile(metric.LatencyBuckets, 0.95)
	if metric.CachePromptTokens > 0 {
		metric.CacheTokenRate = floatPtr(float64(metric.CacheTokensTotal) / float64(metric.CachePromptTokens))
	}
	return metric
}

func truncateTo5m(value time.Time) time.Time {
	minute := value.Minute() - value.Minute()%5
	return time.Date(value.Year(), value.Month(), value.Day(), value.Hour(), minute, 0, 0, value.Location())
}
