package aggregator

import "time"

// 原始日志与 Agent 使用相同的轻量流量计数，不分配交叉桶的耗时样本。
type customerChannelBucket struct {
	instance      string
	bucket        time.Time
	user, channel int64
}

type customerChannelCounts struct {
	tokens, requests, consumes int64
}
