package storage

import (
	"os"
	"strings"
	"testing"
)

func TestMetricIndexMigrationPinsCompositeIndexes(t *testing.T) {
	data, err := os.ReadFile("../../migrations/008_metric_indexes.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	for _, fragment := range []string{
		"idx_metric_1m_dim_bucket ON metric_1m (dimension_type, instance_id, dimension_key, bucket_time)",
		"idx_metric_5m_dim_bucket ON metric_5m (dimension_type, instance_id, dimension_key, bucket_time)",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("008 migration missing %q", fragment)
		}
	}
}
