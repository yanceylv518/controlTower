package mysqlstore

import (
	"context"
	"time"

	"controltower/server/internal/storage"
)

func (s Store) UsageSummary(since time.Time) ([]storage.UsageRow, error) {
	rows, err := s.db.QueryContext(context.Background(), usageSummarySQL(), since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []storage.UsageRow
	for rows.Next() {
		var item storage.UsageRow
		if err := rows.Scan(&item.DimensionType, &item.DimensionKey, &item.RequestCount, &item.PromptTokens, &item.CompletionTokens, &item.Quota); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func usageSummarySQL() string {
	return `SELECT dimension_type, dimension_key, SUM(request_count), SUM(prompt_tokens), SUM(completion_tokens), SUM(quota)
FROM metric_1m
WHERE dimension_type IN ('instance_user', 'instance_channel', 'instance_model') AND bucket_time >= ?
GROUP BY dimension_type, dimension_key
ORDER BY SUM(quota) DESC`
}
