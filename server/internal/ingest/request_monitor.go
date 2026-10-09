package ingest

import (
	"context"
	"controltower/server/internal/aggregator"
	"time"
)

func (s *MemoryStore) RequestMonitorMetrics(ctx context.Context, from, to time.Time) ([]aggregator.Metric, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []aggregator.Metric{}
	for _, m := range s.metrics1m {
		if m.DimensionType == "instance_channel" && !m.BucketTime.Before(from) && m.BucketTime.Before(to) {
			items = append(items, m)
			if len(items) > 10000 {
				break
			}
		}
	}
	return items, nil
}
