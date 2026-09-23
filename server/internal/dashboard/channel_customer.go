package dashboard

import (
	"controltower/server/internal/aggregator"
	"strconv"
	"strings"
)

// Reuse the stored customer/channel buckets without duplicating ingestion.
func channelCustomerMetrics(metrics []aggregator.Metric, instance string) []aggregator.Metric {
	out := make([]aggregator.Metric, 0, len(metrics))
	for _, m := range metrics {
		if m.InstanceID != instance || m.DimensionType != "instance_user_channel" {
			continue
		}
		tail, ok := strings.CutPrefix(m.DimensionKey, instance+":user:")
		user, channel, split := strings.Cut(tail, ":channel:")
		uid, ue := strconv.ParseInt(user, 10, 64)
		cid, ce := strconv.ParseInt(channel, 10, 64)
		if !ok || !split || ue != nil || ce != nil || uid <= 0 || cid <= 0 {
			continue
		}
		m.DimensionType = "instance_channel_user"
		m.DimensionKey = instance + ":channel:" + channel + ":user:" + user
		out = append(out, m)
	}
	return out
}
