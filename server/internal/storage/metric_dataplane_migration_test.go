package storage

import (
	"os"
	"strings"
	"testing"
)

func TestMetricDataplaneMigrationContainsAdditiveNullableColumns(t *testing.T) {
	data, err := os.ReadFile("../../migrations/010_metric_dataplane.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(data)
	for _, fragment := range []string{
		"ALTER TABLE metric_1m", "ALTER TABLE metric_5m", "ALTER TABLE channel_snapshots",
		"p50_use_time DOUBLE NULL", "p99_use_time DOUBLE NULL", "big_input_count BIGINT NULL",
		"big_input_cache_hits BIGINT NULL", "ttft_count BIGINT NULL", "ttft_sum_ms BIGINT NULL",
		"ttft_p95_ms DOUBLE NULL", "group_name VARCHAR(128) NULL", "priority BIGINT NULL",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("010 migration missing %q", fragment)
		}
	}
	// ApplyDir replays every migration file on each server start; a re-pinning
	// ALTER ... ENGINE=... statement succeeds every time and forces a full
	// table rebuild on every startup, so its absence is part of the contract.
	if strings.Contains(sql, "ENGINE=") {
		t.Fatal("010 migration must not contain table-rebuild ALTER statements")
	}
}
