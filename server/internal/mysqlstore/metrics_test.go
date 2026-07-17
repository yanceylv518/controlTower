package mysqlstore

import (
	"database/sql"
	"strings"
	"testing"
)

func TestRecentMetricsSQLReadsLatestMetrics(t *testing.T) {
	sqlText := recentMetricsSQL("metric_1m", false)
	for _, fragment := range []string{
		"FROM metric_1m",
		"ORDER BY bucket_time DESC, dimension_type ASC, dimension_key ASC",
		"LIMIT ?",
	} {
		if !strings.Contains(sqlText, fragment) {
			t.Fatalf("recent metrics query missing %q: %s", fragment, sqlText)
		}
	}
}

func TestLatestMetricsSQLRestrictsToNewestBucket(t *testing.T) {
	sqlText := recentMetricsSQL("metric_1m", true)
	for _, fragment := range []string{"JOIN (", "MAX(bucket_time) AS mb", "bucket_time >= ?", "(? = '' OR dimension_type = ?)", "GROUP BY instance_id, dimension_type, dimension_key", "m.bucket_time=t.mb"} {
		if !strings.Contains(sqlText, fragment) {
			t.Fatalf("latest metrics query missing %q: %s", fragment, sqlText)
		}
	}
}

func TestMetricHistorySQLFiltersAndSorts(t *testing.T) {
	sqlText := metricHistorySQL("metric_5m")
	for _, fragment := range []string{"FROM metric_5m", "dimension_type = ?", "dimension_key = ?", "bucket_time >= ?", "ORDER BY bucket_time ASC"} {
		if !strings.Contains(sqlText, fragment) {
			t.Fatalf("history query missing %q: %s", fragment, sqlText)
		}
	}
}

func TestMetricHistoryPrefixSQLFiltersAndSorts(t *testing.T) {
	sqlText := metricHistoryPrefixSQL("metric_1m")
	for _, fragment := range []string{"FROM metric_1m", "dimension_type = ?", "dimension_key LIKE CONCAT(?, '%')", "bucket_time >= ?", "ORDER BY dimension_key ASC, bucket_time ASC"} {
		if !strings.Contains(sqlText, fragment) {
			t.Fatalf("prefix history query missing %q: %s", fragment, sqlText)
		}
	}
}
func TestFloatPointerConvertsSQLNullFloat(t *testing.T) {
	if got := floatPointer(sql.NullFloat64{}); got != nil {
		t.Fatalf("nil SQL float = %#v, want nil", got)
	}
	got := floatPointer(sql.NullFloat64{Float64: 0.75, Valid: true})
	if got == nil || *got != 0.75 {
		t.Fatalf("valid SQL float = %#v, want 0.75", got)
	}
}
