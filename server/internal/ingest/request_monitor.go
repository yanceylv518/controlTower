package ingest

import (
	"context"
	"controltower/server/internal/aggregator"
	"time"
)

func (s *MemoryStore) RequestMonitorMetrics(ctx context.Context, from, to time.Time, ids ...string) ([]aggregator.Metric, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []aggregator.Metric{}
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	for _, m := range s.metrics1m {
		if (len(ids) == 0 || allowed[m.InstanceID]) && m.DimensionType == "instance_channel" && !m.BucketTime.Before(from) && m.BucketTime.Before(to) {
			items = append(items, m)
			if len(items) > 10000 {
				break
			}
		}
	}
	return items, nil
}
