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
	bigInputCount     int64
	bigInputCacheHits int64
	ttftValues        []float64
	ttftCount         int64
	ttftSumMS         int64
	latencyV2         latencyhist.BucketsV2
	ttftV2            latencyhist.BucketsV2
}

const maxRawValuesPerBucket = 10_000

type dimension struct {
	dimensionType string
	dimensionKey  string
}

func Aggregate(instanceID string, events []logcollector.Event, cacheHitMinPromptTokens int64) []reporter.AggregatedMetricPayload {
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
			acc.add(event, cacheHitMinPromptTokens)
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

func (a *accumulator) add(event logcollector.Event, cacheHitMinPromptTokens int64) {
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
	if len(a.useTimes) < maxRawValuesPerBucket {
		a.useTimes = append(a.useTimes, event.UseTime)
	}
	a.metric.UseTimeSum += event.UseTime
	a.metric.LatencyBuckets[latencyhist.Index(event.UseTime)]++
	a.latencyV2[latencyhist.IndexV2(event.UseTime)]++
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
	if event.LogType == "consume" && event.PromptTokens > cacheHitMinPromptTokens {
		a.bigInputCount++
		if event.CacheTokens != nil && *event.CacheTokens > 0 {
			a.bigInputCacheHits++
		}
	}
	if event.IsStream && event.FirstResponseMs != nil {
		a.ttftCount++
		a.ttftSumMS += *event.FirstResponseMs
		if len(a.ttftValues) < maxRawValuesPerBucket {
			a.ttftValues = append(a.ttftValues, float64(*event.FirstResponseMs))
		}
		a.ttftV2[latencyhist.IndexV2(float64(*event.FirstResponseMs)/1000)]++
		generationSeconds := event.UseTime - float64(*event.FirstResponseMs)/1000
		if event.LogType == "consume" && event.CompletionTokens > 0 && generationSeconds > 0 {
			a.metric.OTPSOutputTokens += event.CompletionTokens
			a.metric.OTPSDurationSecs += generationSeconds
		}
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
		metric.AvgUseTime = floatPtr(metric.UseTimeSum / float64(metric.RequestCount))
		metric.P50UseTime = floatPtr(quantile(a.useTimes, 0.50))
		metric.P95UseTime = floatPtr(quantile(a.useTimes, 0.95))
		metric.P99UseTime = floatPtr(quantile(a.useTimes, 0.99))
	}
	if a.cachePromptTokens > 0 {
		metric.CacheTokenRate = floatPtr(float64(a.cacheTokens) / float64(a.cachePromptTokens))
	}
	metric.BigInputCount = int64Ptr(a.bigInputCount)
	metric.BigInputCacheHits = int64Ptr(a.bigInputCacheHits)
	metric.TTFTCount = int64Ptr(a.ttftCount)
	metric.TTFTSumMS = int64Ptr(a.ttftSumMS)
	if len(a.ttftValues) > 0 {
		metric.TTFTP50MS = floatPtr(quantile(a.ttftValues, 0.50))
		metric.TTFTP90MS = floatPtr(quantile(a.ttftValues, 0.90))
		metric.TTFTP95MS = floatPtr(quantile(a.ttftValues, 0.95))
	}
	metric.LatencyBucketsV2 = a.latencyV2[:]
	if a.ttftCount > 0 {
		metric.TTFTBuckets = a.ttftV2[:]
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

func quantile(values []float64, q float64) float64 {
	copied := append([]float64(nil), values...)
	sort.Float64s(copied)
	index := int(float64(len(copied))*q+0.999999) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(copied) {
		index = len(copied) - 1
	}
	return copied[index]
}

func int64Ptr(value int64) *int64 { return &value }

func floatPtr(value float64) *float64 {
	return &value
}
