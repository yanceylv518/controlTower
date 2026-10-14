package mysqlstore

import (
	"context"
	"controltower/server/internal/aggregator"
	"controltower/server/internal/storage"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestOverviewSnapshotMySQL(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated CT_MYSQL_TEST_DSN")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	// Applying again exercises migration tracking/idempotence.
	if err := ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	store := New(db)
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Truncate(time.Minute).Add(-8 * time.Minute)
	prefix := fmt.Sprintf("overview-qa-%d", now.UnixNano())
	a, b, foreign, deleted := prefix+"a", prefix+"b", prefix+"x", prefix+"d"
	site, other := prefix+"site", prefix+"other"
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{a, b, foreign, deleted} {
		s := site
		if id == foreign {
			s = other
		}
		must(store.CreateInstance(storage.Instance{ID: id, SiteID: s, Name: id, Enabled: true, CreatedAt: now, UpdatedAt: now}))
	}
	// Every test owns a unique prefix; do not touch any other test's rows.
	defer func() {
		for _, table := range []string{"overview_metric_coverage", "overview_metric_sources", "channel_rate_seconds", "metric_1m", "metric_5m", "metric_batches", "health_checks", "docker_statuses", "agents", "alerts"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id IN (?,?,?,?)", a, b, foreign, deleted)
		}
		_, _ = db.Exec("DELETE FROM alert_events WHERE alert_id LIKE ?", prefix+"%")
		_, _ = db.Exec("DELETE FROM instances WHERE id IN (?,?,?,?)", a, b, foreign, deleted)
	}()
	_, err = db.Exec("UPDATE instances SET deleted=1 WHERE id=?", deleted)
	must(err)
	marker := func(at time.Time) aggregator.Metric {
		return aggregator.Metric{BucketTime: at, DimensionType: "channel_rate_second", DimensionKey: "0"}
	}
	for i := 0; i <= 4; i++ {
		must(store.ApplyMetricBatch(a, fmt.Sprint(i), []aggregator.Metric{marker(at.Add(time.Duration(i) * 30 * time.Second))}))
	}
	batch := []aggregator.Metric{{InstanceID: a, BucketTime: at, DimensionType: "instance", DimensionKey: a, RequestCount: 10, SuccessCount: 8, ErrorCount: 2, TPM: 100, PromptTokens: 80, CompletionTokens: 20}}
	must(store.ApplyMetricBatch(a, "requests", batch))
	must(store.ApplyMetricBatch(a, "requests", batch))
	// The other runtime node has no log collection and is not expected coverage.
	ids, cs, err := store.OverviewCoverage(ctx, site, at, at.Add(3*time.Minute))
	must(err)
	if len(ids) != 1 || ids[0] != a || len(cs) != 2 {
		t.Fatalf("coverage ids=%v rows=%+v", ids, cs)
	}
	metrics, err := store.OverviewMetrics(ctx, ids, at, at.Add(time.Minute))
	must(err)
	if len(metrics) != 1 || metrics[0].RequestCount != 10 || metrics[0].TPM != 100 {
		t.Fatalf("batch dedup/range: %+v", metrics)
	}
	metrics, err = store.OverviewMetrics(ctx, ids, at.Add(time.Minute), at.Add(2*time.Minute))
	must(err)
	if len(metrics) != 0 {
		t.Fatal("query crossed requested time window")
	}
	// A restart after missing reports must not fill an unobserved interval.
	must(store.ApplyMetricBatch(a, "gap", []aggregator.Metric{marker(at.Add(5 * time.Minute))})) // report gap is not treated as continuous coverage
	_, err = db.Exec("DELETE FROM channel_rate_seconds WHERE instance_id=?", a)
	must(err)
	ids, cs, err = store.OverviewCoverage(ctx, site, at, now)
	must(err)
	if len(ids) != 1 || len(cs) != 2 {
		t.Fatal("quiet collector disappeared with expired seconds")
	}
	must(store.InsertHealthCheck(storage.HealthCheck{InstanceID: b, Target: "status", CheckedAt: at, Status: "up", HTTPStatusCode: 200}))
	for i := 0; i < 205; i++ {
		must(store.InsertHealthCheck(storage.HealthCheck{InstanceID: a, Target: "status", CheckedAt: now.Add(-time.Duration(i) * time.Second), Status: "up", HTTPStatusCode: 200}))
	}
	must(store.InsertHealthCheck(storage.HealthCheck{InstanceID: foreign, Target: "foreign", CheckedAt: now, Status: "down"}))
	must(store.InsertDockerStatus(storage.DockerStatus{InstanceID: a, ContainerName: "api", CollectedAt: now, Running: true, Status: "running"}))
	runtime, err := store.OverviewRuntime(ctx, site)
	must(err)
	if len(runtime) != 2 {
		t.Fatalf("site/deleted isolation: %+v", runtime)
	}
	for _, row := range runtime {
		if len(row.Health) != 1 {
			t.Fatalf("history limit hid a node: %+v", row)
		}
		if row.ID == b && len(row.Docker) != 0 {
			t.Fatal("unreported containers fabricated")
		}
	}
	var alerts []storage.Alert
	for i := 0; i < 60; i++ {
		alerts = append(alerts, storage.Alert{ID: fmt.Sprintf("%sforeign%d", prefix, i), InstanceID: foreign, RuleKey: "error_rate", Title: "other", Severity: "critical", LastSeenAt: now})
	}
	for i := 0; i < 8; i++ {
		stamp := now
		if i >= 2 {
			stamp = now.Add(-48 * time.Hour)
		}
		alerts = append(alerts, storage.Alert{ID: fmt.Sprintf("%slocal%d", prefix, i), InstanceID: a, RuleKey: "error_rate", Title: "local", Severity: "warning", LastSeenAt: stamp, DimensionType: "instance_channel", DimensionKey: a + ":channel:7"})
	}
	must(store.UpsertCurrentAlerts(alerts, now))
	must(store.MarkOverviewAlertEvaluation(now))
	snapshot, err := store.OverviewAlerts(ctx, site, now)
	must(err)
	if snapshot.Total != 8 || snapshot.Unconfirmed != 6 || len(snapshot.Items) != 2 || snapshot.Items[0].DimensionKey != a+":channel:7" {
		t.Fatalf("alert freshness/site: %+v", snapshot)
	}
	list, err := store.QueryAlerts(storage.AlertQuery{SiteID: site, ActiveOnly: true, Limit: 5})
	must(err)
	if len(list) != 5 {
		t.Fatalf("site pagination: %d", len(list))
	}
	for _, item := range list {
		if item.InstanceID != a {
			t.Fatal("cross-site alert")
		}
	}
	snapshot, err = store.OverviewAlerts(ctx, site, now.Add(3*time.Minute))
	must(err)
	if snapshot.Unconfirmed != 8 || len(snapshot.Items) != 0 {
		t.Fatal("stale evaluation remained current")
	}
}
