package metricaggregator

import (
	"sort"
	"strconv"
	"time"

	"controltower/agent/internal/logcollector"
	"controltower/agent/internal/reporter"
	"controltower/internal/latencyhist"
)

type accumulator struct {
	metric            reporter.AggregatedMetricPayload
	useTimes          []float64
	streamCount       int64
	cacheTokens       int64
	cachePromptTokens int64
}

type dimension struct {
	dimensionType string
	dimensionKey  string
}

func Aggregate(instanceID string, events []logcollector.Event) []reporter.AggregatedMetricPayload {
	accumulators := make(map[string]*accumulator)
	for _, event := range events {
		bucket := event.CreatedAt.Truncate(time.Minute)
		for _, dimension := range dimensionsFor(instanceID, event) {
			key := bucket.Format(time.RFC3339) + "|" + dimension.dimensionType + "|" + dimension.dimensionKey
			acc := accumulators[key]
			if acc == nil {
				acc = &accumulator{metric: reporter.AggregatedMetricPayload{BucketTime: bucket, WindowSeconds: 60, DimensionType: dimension.dimensionType, DimensionKey: dimension.dimensionKey}}
				accumulators[key] = acc
			}
			acc.add(event)
		}
	}

	metrics := make([]reporter.AggregatedMetricPayload, 0, len(accumulators))
	for _, acc := range accumulators {
		metrics = append(metrics, acc.finalize())
	}
	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i].BucketTime.Equal(metrics[j].BucketTime) {
			if metrics[i].DimensionType == metrics[j].DimensionType {
				return metrics[i].DimensionKey < metrics[j].DimensionKey
			}
			return metrics[i].DimensionType < metrics[j].DimensionType
		}
		return metrics[i].BucketTime.Before(metrics[j].BucketTime)
	})
	return metrics
}

func (a *accumulator) add(event logcollector.Event) {
	a.metric.RequestCount++
	if event.LogType == "consume" {
		a.metric.SuccessCount++
	}
	if event.LogType == "error" {
		a.metric.ErrorCount++
	}
	a.metric.TPM += event.TotalTokens
	a.metric.PromptTokens += event.PromptTokens
	a.metric.CompletionTokens += event.CompletionTokens
	a.metric.Quota += event.Quota
	a.useTimes = append(a.useTimes, event.UseTime)
	a.metric.UseTimeSum += event.UseTime
	a.metric.LatencyBuckets[latencyhist.Index(event.UseTime)]++
	if event.IsStream {
		a.streamCount++
		a.metric.StreamCount++
	}
	if event.CacheFieldPresent && event.CacheTokens != nil {
		a.cacheTokens += *event.CacheTokens
		a.cachePromptTokens += event.PromptTokens
		a.metric.CacheTokensTotal += *event.CacheTokens
		a.metric.CachePromptTokens += event.PromptTokens
	}
}

func (a *accumulator) finalize() reporter.AggregatedMetricPayload {
	metric := a.metric
	if metric.RequestCount > 0 {
		metric.SuccessRate = ratio(metric.SuccessCount, metric.RequestCount)
		metric.ErrorRate = ratio(metric.ErrorCount, metric.RequestCount)
		metric.StreamRate = ratio(a.streamCount, metric.RequestCount)
	}
	if len(a.useTimes) > 0 {
		metric.AvgUseTime = floatPtr(avg(a.useTimes))
		metric.P95UseTime = latencyhist.Quantile(metric.LatencyBuckets, 0.95)
	}
	if a.cachePromptTokens > 0 {
		metric.CacheTokenRate = floatPtr(float64(a.cacheTokens) / float64(a.cachePromptTokens))
	}
	return metric
}

func dimensionsFor(instanceID string, event logcollector.Event) []dimension {
	dimensions := []dimension{{dimensionType: "instance", dimensionKey: instanceID}}
	if event.UserID > 0 {
		dimensions = append(dimensions, dimension{dimensionType: "instance_user", dimensionKey: instanceID + ":user:" + strconv.FormatInt(event.UserID, 10)})
	}
	if event.ChannelID > 0 {
		dimensions = append(dimensions, dimension{dimensionType: "instance_channel", dimensionKey: instanceID + ":channel:" + strconv.FormatInt(event.ChannelID, 10)})
	}
	if event.ModelName != "" {
		dimensions = append(dimensions, dimension{dimensionType: "instance_model", dimensionKey: instanceID + ":model:" + event.ModelName})
	}
	if event.UserID > 0 && event.ModelName != "" {
		dimensions = append(dimensions, dimension{dimensionType: "instance_user_model", dimensionKey: instanceID + ":user:" + strconv.FormatInt(event.UserID, 10) + ":model:" + event.ModelName})
		dimensions = append(dimensions, dimension{dimensionType: "instance_model_user", dimensionKey: instanceID + ":model:" + event.ModelName + ":user:" + strconv.FormatInt(event.UserID, 10)})
	}
	if event.ChannelID > 0 && event.ModelName != "" {
		dimensions = append(dimensions, dimension{dimensionType: "instance_channel_model", dimensionKey: instanceID + ":channel:" + strconv.FormatInt(event.ChannelID, 10) + ":model:" + event.ModelName})
		dimensions = append(dimensions, dimension{dimensionType: "instance_model_channel", dimensionKey: instanceID + ":model:" + event.ModelName + ":channel:" + strconv.FormatInt(event.ChannelID, 10)})
	}
	return dimensions
}

func LatencyBucketIndex(seconds float64) int {
	return latencyhist.Index(seconds)
}
func ratio(numerator int64, denominator int64) *float64 {
	if denominator == 0 {
		return nil
	}
	return floatPtr(float64(numerator) / float64(denominator))
}

func avg(values []float64) float64 {
	var total float64
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func p95(values []float64) float64 {
	copied := append([]float64(nil), values...)
	sort.Float64s(copied)
	index := int(float64(len(copied))*0.95+0.999999) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(copied) {
		index = len(copied) - 1
	}
	return copied[index]
}

func floatPtr(value float64) *float64 {
	return &value
}
