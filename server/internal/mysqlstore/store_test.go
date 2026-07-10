package mysqlstore

import (
	"strings"
	"testing"
	"time"

	"controltower/server/internal/aggregator"
	"controltower/server/internal/dashboard"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
)

func TestStoreImplementsControlTowerStoreInterfaces(t *testing.T) {
	var store Store
	var _ ingest.Store = store
	var _ dashboard.LogStore = store
	var _ dashboard.RuntimeStore = store
	var _ dashboard.AlertStore = store
	var _ dashboard.NotificationStore = store
	var _ aggregator.MetricStore = store
	var _ aggregator.EventSource = store
}

func TestBuildLogQueryUsesFiltersOrderAndPagination(t *testing.T) {
	start := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	sqlText, args := buildLogQuery(storage.LogQuery{
		InstanceID: "inst-1",
		UserID:     7,
		ChannelID:  18,
		ModelName:  "gpt-4o",
		LogType:    "error",
		RequestID:  "req-1",
		StartTime:  start,
		EndTime:    end,
		Limit:      20,
		Offset:     5,
	})

	for _, fragment := range []string{
		"FROM log_events",
		"instance_id = ?",
		"user_id = ?",
		"channel_id = ?",
		"model_name = ?",
		"log_type = ?",
		"request_id = ?",
		"created_at >= ?",
		"created_at <= ?",
		"ORDER BY created_at DESC, source_log_id DESC LIMIT ? OFFSET ?",
	} {
		if !strings.Contains(sqlText, fragment) {
			t.Fatalf("query missing %q: %s", fragment, sqlText)
		}
	}
	if len(args) != 10 {
		t.Fatalf("args len = %d, want 10: %#v", len(args), args)
	}
	if args[len(args)-2] != 20 || args[len(args)-1] != 5 {
		t.Fatalf("pagination args = %#v, want limit 20 offset 5", args[len(args)-2:])
	}
}

func TestBuildLogQueryCapsLimitAndNormalizesOffset(t *testing.T) {
	_, args := buildLogQuery(storage.LogQuery{Limit: 1000, Offset: -10})
	if args[len(args)-2] != storage.MaxLogQueryLimit {
		t.Fatalf("limit arg = %#v, want cap %d", args[len(args)-2], storage.MaxLogQueryLimit)
	}
	if args[len(args)-1] != 0 {
		t.Fatalf("offset arg = %#v, want 0", args[len(args)-1])
	}
}

func TestMetricUpsertSQLUsesMySQLIdempotentUpsert(t *testing.T) {
	sqlText := metricUpsertSQL("metric_1m")
	for _, fragment := range []string{
		"INSERT INTO metric_1m",
		"ON DUPLICATE KEY UPDATE",
		"request_count = VALUES(request_count)",
		"cache_token_rate = VALUES(cache_token_rate)",
	} {
		if !strings.Contains(sqlText, fragment) {
			t.Fatalf("metric upsert missing %q: %s", fragment, sqlText)
		}
	}
}

func TestMetricArgsConvertsNilRatesToSQLNulls(t *testing.T) {
	args := metricArgs(aggregator.Metric{
		InstanceID:    "inst-1",
		BucketTime:    time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC),
		DimensionType: "instance",
		DimensionKey:  "inst-1",
		RequestCount:  5,
	})
	if len(args) != 32 {
		t.Fatalf("metric args len = %d, want 32", len(args))
	}
	for _, idx := range []int{7, 8, 13, 14, 15, 16} {
		value, ok := args[idx].(interface{ IsZero() bool })
		if ok && !value.IsZero() {
			t.Fatalf("arg %d should be zero SQL null: %#v", idx, args[idx])
		}
	}
}
func TestBuildRuntimeQueriesUseFiltersOrderAndPagination(t *testing.T) {
	start := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	metricSQL, metricArgs := buildServerMetricQuery(storage.ServerMetricQuery{InstanceID: "inst-1", StartTime: start, EndTime: end, Limit: 20, Offset: 5})
	for _, fragment := range []string{"FROM server_metrics_10s", "instance_id = ?", "collected_at >= ?", "collected_at <= ?", "ORDER BY collected_at DESC", "LIMIT ? OFFSET ?"} {
		if !strings.Contains(metricSQL, fragment) {
			t.Fatalf("metric query missing %q: %s", fragment, metricSQL)
		}
	}
	if len(metricArgs) != 5 || metricArgs[len(metricArgs)-2] != 20 || metricArgs[len(metricArgs)-1] != 5 {
		t.Fatalf("unexpected metric args: %#v", metricArgs)
	}

	healthSQL, healthArgs := buildHealthCheckQuery(storage.HealthCheckQuery{InstanceID: "inst-1", Target: "http://status", Status: "up", StartTime: start, EndTime: end, Limit: 10})
	for _, fragment := range []string{"FROM health_checks", "target = ?", "status = ?", "ORDER BY checked_at DESC, target ASC"} {
		if !strings.Contains(healthSQL, fragment) {
			t.Fatalf("health query missing %q: %s", fragment, healthSQL)
		}
	}
	if len(healthArgs) != 7 || healthArgs[len(healthArgs)-2] != 10 || healthArgs[len(healthArgs)-1] != 0 {
		t.Fatalf("unexpected health args: %#v", healthArgs)
	}

	running := true
	dockerSQL, dockerArgs := buildDockerStatusQuery(storage.DockerStatusQuery{InstanceID: "inst-1", ContainerName: "new-api", Running: &running, Limit: 300, Offset: -1})
	for _, fragment := range []string{"FROM docker_statuses", "container_name = ?", "running = ?", "ORDER BY collected_at DESC, container_name ASC"} {
		if !strings.Contains(dockerSQL, fragment) {
			t.Fatalf("docker query missing %q: %s", fragment, dockerSQL)
		}
	}
	if dockerArgs[len(dockerArgs)-2] != storage.MaxRuntimeQueryLimit || dockerArgs[len(dockerArgs)-1] != 0 {
		t.Fatalf("docker pagination args = %#v", dockerArgs[len(dockerArgs)-2:])
	}
}

func TestBuildAlertQuerySupportsActiveOnly(t *testing.T) {
	sqlText, args := buildAlertQuery(storage.AlertQuery{Status: "firing", Severity: "critical", ActiveOnly: true, Limit: 10})
	for _, fragment := range []string{"FROM alerts", "status = ?", "severity = ?", "status <> ?", "ORDER BY FIELD(severity"} {
		if !strings.Contains(sqlText, fragment) {
			t.Fatalf("alert query missing %q: %s", fragment, sqlText)
		}
	}
	if len(args) != 5 || args[0] != "firing" || args[1] != "critical" || args[2] != "resolved" || args[3] != 10 || args[4] != 0 {
		t.Fatalf("unexpected alert args: %#v", args)
	}
}
func TestMetricBatchMergeSQLAccumulatesCountsAndDerivedRates(t *testing.T) {
	sqlText := metricBatchMergeSQL("metric_1m", 1)
	for _, fragment := range []string{
		"request_count = request_count + VALUES(request_count)",
		"success_rate = (success_count + VALUES(success_count))",
		"use_time_sum = use_time_sum + VALUES(use_time_sum)",
		"cache_prompt_tokens = cache_prompt_tokens + VALUES(cache_prompt_tokens)",
		"latency_le_250ms = latency_le_250ms + VALUES(latency_le_250ms)",
		"CEIL((",
	} {
		if !strings.Contains(sqlText, fragment) {
			t.Fatalf("metric merge missing %q: %s", fragment, sqlText)
		}
	}
}

func TestMetricBatchUpsertSQLAndArgs(t *testing.T) {
	sqlText := metricBatchUpsertSQL("metric_1m", 2)
	if strings.Count(sqlText, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)") != 2 {
		t.Fatalf("expected two value rows in %s", sqlText)
	}
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	args := metricBatchArgs([]aggregator.Metric{{InstanceID: "inst-1", BucketTime: now}, {InstanceID: "inst-2", BucketTime: now}})
	if len(args) != 64 {
		t.Fatalf("args len = %d, want 64", len(args))
	}
}
