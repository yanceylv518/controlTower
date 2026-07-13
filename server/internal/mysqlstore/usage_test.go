package mysqlstore

import (
	"strings"
	"testing"
)

func TestUsageSummarySQLAggregatesSupportedDimensions(t *testing.T) {
	sqlText := usageSummarySQL()
	for _, fragment := range []string{"FROM metric_1m", "dimension_type IN ('instance_user', 'instance_channel', 'instance_model')", "bucket_time >= ?", "GROUP BY dimension_type, dimension_key", "ORDER BY SUM(quota) DESC"} {
		if !strings.Contains(sqlText, fragment) {
			t.Fatalf("usage query missing %q: %s", fragment, sqlText)
		}
	}
}
