package mysqlstore

import (
	"controltower/internal/latencyhist"
	"controltower/internal/speedstats"
	"controltower/server/internal/aggregator"
	"database/sql"
	"strings"
	"testing"
)

func TestSpeedSQLStorageContract(t *testing.T) {
	s := speedstats.New()
	s.Buckets[3] = 8
	s.RetryCount = 2
	s.UnknownCount = 1
	args := metricArgs(aggregator.Metric{SpeedTTFT: s})
	if len(args) != strings.Count(metricValuePlaceholders(), "?") {
		t.Fatal("SQL placeholders do not match args")
	}
	for _, merge := range []bool{false, true} {
		statements := strings.Join(speedTTFTAssignments(merge), ",")
		for _, c := range speedTTFTColumns() {
			if !strings.Contains(statements, c+" = ") {
				t.Fatal(c)
			}
		}
		if merge && !strings.Contains(statements, "COALESCE(VALUES(speed_ttft2_le_2s),0)") {
			t.Fatal("legacy NULL poisons new evidence")
		}
	}
	var values [latencyhist.BucketCountV2 + 2]sql.NullInt64
	if speedTTFTFromSQL(values) != nil {
		t.Fatal("legacy data became evidence")
	}
	for i, v := range speedTTFTArgs(s) {
		values[i] = sql.NullInt64{Int64: v.(int64), Valid: true}
	}
	roundtrip := speedTTFTFromSQL(values)
	if roundtrip.Samples() != 8 || roundtrip.RetryCount != 2 || roundtrip.UnknownCount != 1 {
		t.Fatal(roundtrip)
	}
}
