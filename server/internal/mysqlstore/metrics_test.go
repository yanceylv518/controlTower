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
	if !strings.Contains(sqlText, "WHERE bucket_time = (SELECT MAX(bucket_time) FROM metric_1m)") {
		t.Fatalf("latest metrics query missing newest-bucket filter: %s", sqlText)
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
