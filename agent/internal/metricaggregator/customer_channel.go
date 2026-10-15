package metricaggregator

import (
	"controltower/agent/internal/reporter"
	"time"
)

// 交叉桶只累计 Token 和请求数，避免分配耗时样本和直方图。
type customerChannelBucket struct {
	bucket        time.Time
	user, channel int64
}

type customerChannelCounts struct {
	tokens, requests, consumes int64
}

// Protect existing metrics when the optional breakdown exceeds the report's
// row budget. Drop the whole breakdown batch so no partial composition is sent.
// Existing metrics, including channel-rate coverage markers, are never dropped.
func LimitCustomerTraffic(metrics []reporter.AggregatedMetricPayload, limit int) ([]reporter.AggregatedMetricPayload, int) {
	if len(metrics) <= limit {
		return metrics, 0
	}
	out := metrics[:0]
	dropped := 0
	for _, metric := range metrics {
		if metric.DimensionType == "instance_user_channel" {
			dropped++
			continue
		}
		out = append(out, metric)
	}
	clear(metrics[len(out):])
	return out, dropped
}
