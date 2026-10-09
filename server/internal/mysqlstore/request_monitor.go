package mysqlstore

import (
	"context"
	"controltower/server/internal/aggregator"
	"strings"
	"time"
)

func (s Store) RequestMonitorMetrics(ctx context.Context, from, to time.Time, ids ...string) ([]aggregator.Metric, error) {
	base := metricHistoryPrefixSQL("metric_1m")
	args := []any{"instance_channel"}
	if len(ids) > 0 {
		base = metricHistoryPrefixInstancesSQL("metric_1m", len(ids))
		for _, id := range ids {
			args = append(args, id)
		}
	}
	args = append(args, "", from, to)
	query := strings.Replace(base, "ORDER BY dimension_key ASC, bucket_time ASC", "AND bucket_time < ? ORDER BY dimension_key ASC, bucket_time ASC LIMIT 10001", 1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMetrics(rows)
}
