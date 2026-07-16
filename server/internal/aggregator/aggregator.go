package aggregator

import (
	"sort"
	"strconv"
	"time"

	"controltower/internal/latencyhist"
	"controltower/server/internal/storage"
)

type Metric struct {
	InstanceID        string
	BucketTime        time.Time
	DimensionType     string
	DimensionKey      string
	RequestCount      int64
	SuccessCount      int64
	ErrorCount        int64
	SuccessRate       *float64
	ErrorRate         *float64
	TPM               int64
	PromptTokens      int64
	CompletionTokens  int64
	Quota             int64
	AvgUseTime        *float64
	P50UseTime        *float64
	P95UseTime        *float64
	P99UseTime        *float64
	StreamRate        *float64
	CacheTokenRate    *float64
	UseTimeSum        float64
	StreamCount       int64
	CacheTokensTotal  int64
	CachePromptTokens int64
	BigInputCount     *int64
	BigInputCacheHits *int64
	TTFTCount         *int64
	TTFTSumMS         *int64
	TTFTP95MS         *float64
	LatencyBuckets    latencyhist.Buckets
}

type accumulator struct {
	metric            Metric
	useTimes          []float64
	streamCount       int64
	cacheTokens       int64
	cachePromptTokens int64
}

func Aggregate1m(events []storage.LogEvent) []Metric {
	accumulators := make(map[string]*accumulator)
	for _, event := range events {
		for _, dimension := range dimensionsFor(event) {
			key := event.InstanceID + "|" + event.CreatedAt.Truncate(time.Minute).Format(time.RFC3339) + "|" + dimension.dimensionType + "|" + dimension.dimensionKey
			acc := accumulators[key]
			if acc == nil {
				acc = &accumulator{
					metric: Metric{
						InstanceID:    event.InstanceID,
						BucketTime:    event.CreatedAt.Truncate(time.Minute),
						DimensionType: dimension.dimensionType,
						DimensionKey:  dimension.dimensionKey,
					},
				}
				accumulators[key] = acc
			}
			acc.add(event)
		}
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

func (a *accumulator) add(event storage.LogEvent) {
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

func (a *accumulator) finalize() Metric {
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

type dimension struct {
	dimensionType string
	dimensionKey  string
}

func dimensionsFor(event storage.LogEvent) []dimension {
	dimensions := []dimension{
		{dimensionType: "instance", dimensionKey: event.InstanceID},
	}
	if event.UserID > 0 {
		dimensions = append(dimensions, dimension{
			dimensionType: "instance_user",
			dimensionKey:  event.InstanceID + ":user:" + strconv.FormatInt(event.UserID, 10),
		})
	}
	if event.ChannelID > 0 {
		dimensions = append(dimensions, dimension{
			dimensionType: "instance_channel",
			dimensionKey:  event.InstanceID + ":channel:" + strconv.FormatInt(event.ChannelID, 10),
		})
	}
	if event.ModelName != "" {
		dimensions = append(dimensions, dimension{
			dimensionType: "instance_model",
			dimensionKey:  event.InstanceID + ":model:" + event.ModelName,
		})
	}
	if event.UserID > 0 && event.ModelName != "" {
		dimensions = append(dimensions, dimension{
			dimensionType: "instance_user_model",
			dimensionKey:  event.InstanceID + ":user:" + strconv.FormatInt(event.UserID, 10) + ":model:" + event.ModelName,
		})
		dimensions = append(dimensions, dimension{
			dimensionType: "instance_model_user",
			dimensionKey:  event.InstanceID + ":model:" + event.ModelName + ":user:" + strconv.FormatInt(event.UserID, 10),
		})
	}
	if event.ChannelID > 0 && event.ModelName != "" {
		dimensions = append(dimensions, dimension{
			dimensionType: "instance_channel_model",
			dimensionKey:  event.InstanceID + ":channel:" + strconv.FormatInt(event.ChannelID, 10) + ":model:" + event.ModelName,
		})
		dimensions = append(dimensions, dimension{
			dimensionType: "instance_model_channel",
			dimensionKey:  event.InstanceID + ":model:" + event.ModelName + ":channel:" + strconv.FormatInt(event.ChannelID, 10),
		})
	}
	return dimensions
}

func MergeMetric(current Metric, incoming Metric) Metric {
	if current.InstanceID == "" {
		return incoming
	}
	merged := current
	merged.RequestCount += incoming.RequestCount
	merged.SuccessCount += incoming.SuccessCount
	merged.ErrorCount += incoming.ErrorCount
	merged.TPM += incoming.TPM
	merged.PromptTokens += incoming.PromptTokens
	merged.CompletionTokens += incoming.CompletionTokens
	merged.Quota += incoming.Quota
	merged.UseTimeSum += incoming.UseTimeSum
	merged.StreamCount += incoming.StreamCount
	merged.CacheTokensTotal += incoming.CacheTokensTotal
	merged.CachePromptTokens += incoming.CachePromptTokens
	merged.BigInputCount = addNullableInt64(merged.BigInputCount, incoming.BigInputCount)
	merged.BigInputCacheHits = addNullableInt64(merged.BigInputCacheHits, incoming.BigInputCacheHits)
	merged.TTFTCount = addNullableInt64(merged.TTFTCount, incoming.TTFTCount)
	merged.TTFTSumMS = addNullableInt64(merged.TTFTSumMS, incoming.TTFTSumMS)
	merged.LatencyBuckets = latencyhist.Add(merged.LatencyBuckets, incoming.LatencyBuckets)
	merged.SuccessRate = ratio(merged.SuccessCount, merged.RequestCount)
	merged.ErrorRate = ratio(merged.ErrorCount, merged.RequestCount)
	merged.StreamRate = ratio(merged.StreamCount, merged.RequestCount)
	if merged.RequestCount > 0 {
		merged.AvgUseTime = floatPtr(merged.UseTimeSum / float64(merged.RequestCount))
	}
	if merged.CachePromptTokens > 0 {
		merged.CacheTokenRate = floatPtr(float64(merged.CacheTokensTotal) / float64(merged.CachePromptTokens))
	}
	merged.P95UseTime = latencyhist.Quantile(merged.LatencyBuckets, 0.95)
	merged.P50UseTime = latencyhist.Quantile(merged.LatencyBuckets, 0.50)
	merged.P99UseTime = latencyhist.Quantile(merged.LatencyBuckets, 0.99)
	merged.TTFTP95MS = maxNullableFloat64(current.TTFTP95MS, incoming.TTFTP95MS)
	return merged
}

func addNullableInt64(left, right *int64) *int64 {
	if left == nil && right == nil {
		return nil
	}
	var value int64
	if left != nil {
		value += *left
	}
	if right != nil {
		value += *right
	}
	return &value
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
