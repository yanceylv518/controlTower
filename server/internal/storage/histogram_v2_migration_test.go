package storage

import (
	"os"
	"strings"
	"testing"
)

func TestHistogramV2MigrationIsAdditiveAndSafe(t *testing.T) {
	data, err := os.ReadFile("../../migrations/012_histogram_v2.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(data)
	for _, fragment := range []string{
		"ALTER TABLE metric_1m", "ALTER TABLE metric_5m",
		"ttft_p50_ms DOUBLE NULL", "ttft_p90_ms DOUBLE NULL",
		"latency2_le_250ms BIGINT NULL", "latency2_gt_90s BIGINT NULL",
		"ttft2_le_250ms BIGINT NULL", "ttft2_gt_90s BIGINT NULL",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("012 migration missing %q", fragment)
		}
	}
	// ApplyDir replays every file on each start; a re-pinning ALTER would
	// force a full table rebuild every time, so its absence is contractual.
	if strings.Contains(sql, "ENGINE=") {
		t.Fatal("012 must not contain table-rebuild ALTER statements")
	}
}
