package storage

import (
	"os"
	"strings"
	"testing"
)

func TestInitialMigrationContainsRequiredTablesAndIndexes(t *testing.T) {
	data, err := os.ReadFile("../../migrations/001_init.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := strings.ToLower(string(data))

	for _, table := range []string{
		"instances",
		"agents",
		"log_offsets",
		"log_events",
		"log_samples",
		"server_metrics_10s",
		"metric_1m",
		"metric_5m",
		"alerts",
		"notification_channels",
		"operation_audits",
		"channel_snapshots",
		"weight_adjustments",
	} {
		if !strings.Contains(sql, "create table if not exists "+table) {
			t.Fatalf("missing table %s", table)
		}
	}

	for _, fragment := range []string{
		"primary key (instance_id, source_log_id)",
		"idx_log_events_instance_created",
		"idx_log_samples_instance_created",
		"idx_metric_1m_bucket_dimension",
		"idx_metric_5m_bucket_dimension",
		"idx_alerts_status_severity",
		"source_latest_log_id bigint",
		"backlog_estimate bigint",
		"latency_le_250ms bigint",
		"latency_gt_60s bigint",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("missing schema fragment %q", fragment)
		}
	}
}

func TestInitialMigrationUsesExplicitMySQLTypesAndTableOptions(t *testing.T) {
	data, err := os.ReadFile("../../migrations/001_init.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := strings.ToLower(string(data))

	for _, required := range []string{
		"datetime(6)",
		"tinyint(1)",
		" double ",
		"engine=innodb",
		"default charset=utf8mb4",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration missing mysql fragment %q", required)
		}
	}
}

func TestInitialMigrationDoesNotUseUnsupportedMySQLFragments(t *testing.T) {
	data, err := os.ReadFile("../../migrations/001_init.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := strings.ToLower(string(data))

	for _, forbidden := range []string{"jsonb", "serial", "auto_increment", "double precision", "boolean"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("migration uses forbidden fragment %q", forbidden)
		}
	}
}
