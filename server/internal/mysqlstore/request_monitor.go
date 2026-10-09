package mysqlstore

import (
	"context"
	"controltower/server/internal/aggregator"
	"strings"
	"time"
)

func (s Store) RequestMonitorMetrics(ctx context.Context, from, to time.Time) ([]aggregator.Metric, error) {
	query := strings.Replace(metricHistoryPrefixSQL("metric_1m"), "ORDER BY dimension_key ASC, bucket_time ASC", "AND bucket_time < ? ORDER BY dimension_key ASC, bucket_time ASC LIMIT 10001", 1)
	rows, err := s.db.QueryContext(ctx, query, "instance_channel", "", from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMetrics(rows)
}
