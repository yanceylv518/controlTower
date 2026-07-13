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
	for _, fragment := range []string{"MAX(candidate.bucket_time)", "candidate.dimension_type = current.dimension_type", "candidate.dimension_key = current.dimension_key"} {
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
func TestFloatPointerConvertsSQLNullFloat(t *testing.T) {
	if got := floatPointer(sql.NullFloat64{}); got != nil {
		t.Fatalf("nil SQL float = %#v, want nil", got)
	}
	got := floatPointer(sql.NullFloat64{Float64: 0.75, Valid: true})
	if got == nil || *got != 0.75 {
		t.Fatalf("valid SQL float = %#v, want 0.75", got)
	}
}
