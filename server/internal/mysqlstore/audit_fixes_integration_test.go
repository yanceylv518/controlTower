package mysqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"controltower/server/internal/aggregator"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

func auditFixesDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated CT_MYSQL_TEST_DSN")
	}
	cfg, err := mysql.ParseDSN(dsn)
	require.NoError(t, err)
	admin, err := OpenForMigrations(dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("ct_audit_fix_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE `" + schema + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
	require.NoError(t, err)
	cfg.DBName = schema
	cfg.ParseTime = true
	db, err := OpenForMigrations(cfg.FormatDSN())
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		_, e := admin.Exec("DROP DATABASE `" + schema + "`")
		require.NoError(t, e)
		admin.Close()
	})
	return db
}

func TestAuditMigrationRecoveryAfterDDLBeforeLedger(t *testing.T) {
	for _, tc := range []struct{ version, table, insert string }{
		{"123_billing_excluded_output_stats.sql", "billing_excluded_output_stats", "INSERT INTO billing_excluded_output_stats(job_id,model_name,request_count) VALUES('job','Model',7)"},
		{"133_billing_tier_statistics.sql", "billing_tier_statistics", "INSERT INTO billing_tier_statistics VALUES('job','2026-10-01',1,'{}')"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			db := auditFixesDatabase(t)
			ctx := context.Background()
			_, err := db.Exec(`CREATE TABLE billing_jobs(id VARCHAR(40) PRIMARY KEY) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`)
			require.NoError(t, err)
			_, err = db.Exec(`INSERT INTO billing_jobs VALUES('job')`)
			require.NoError(t, err)
			raw, err := os.ReadFile(filepath.Join("../../migrations", tc.version))
			require.NoError(t, err)
			// Exact published DDL committed, ledger write never happened.
			require.NoError(t, ApplySQL(ctx, db, string(raw)))
			_, err = db.Exec(tc.insert)
			require.NoError(t, err)
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, tc.version), raw, 0600))
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); errs <- ApplyDir(ctx, db, dir) }()
			}
			wg.Wait()
			close(errs)
			for e := range errs {
				require.NoError(t, e)
			}
			var count int
			require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+tc.table).Scan(&count))
			require.Equal(t, 1, count)
			require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, tc.version).Scan(&count))
			require.Equal(t, 1, count)
			// Loss of the ledger is recoverable again and never overwrites rows.
			_, err = db.Exec(`DELETE FROM schema_migrations WHERE version=?`, tc.version)
			require.NoError(t, err)
			require.NoError(t, ApplyDir(ctx, db, dir))
			// A structurally incompatible table must NOT be marked migrated.
			_, err = db.Exec(`DELETE FROM schema_migrations WHERE version=?`, tc.version)
			require.NoError(t, err)
			if tc.table == "billing_excluded_output_stats" {
				_, err = db.Exec(`ALTER TABLE billing_excluded_output_stats MODIFY total_amount DECIMAL(10,2) NOT NULL DEFAULT 0`)
			} else {
				_, err = db.Exec(`ALTER TABLE billing_tier_statistics DROP FOREIGN KEY fk_billing_tier_statistics_job`)
			}
			require.NoError(t, err)
			require.ErrorContains(t, ApplyDir(ctx, db, dir), "cannot recover migration")
			require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, tc.version).Scan(&count))
			require.Zero(t, count)
		})
	}
}

func TestAuditMigrationRecoveryDoesNotIgnoreOtherCreateFailures(t *testing.T) {
	db := auditFixesDatabase(t)
	ctx := context.Background()
	_, err := db.Exec(`CREATE TABLE existing_table(id INT PRIMARY KEY)`)
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "999_unknown.sql"), []byte(`CREATE TABLE existing_table(id INT PRIMARY KEY);`), 0600))
	require.ErrorContains(t, ApplyDir(ctx, db, dir), "already exists")
}

func TestAuditMetricBatchExpiryPreservesIdempotencyAndLegacy(t *testing.T) {
	db := auditFixesDatabase(t)
	ctx := context.Background()
	require.NoError(t, ApplyDir(ctx, db, "../../migrations"))
	s := New(db)
	var now time.Time
	require.NoError(t, db.QueryRow(`SELECT UTC_TIMESTAMP(6)`).Scan(&now))
	now = now.Truncate(time.Minute)
	metric := func(at time.Time, n int64) aggregator.Metric {
		return aggregator.Metric{InstanceID: "audit", BucketTime: at, DimensionType: "instance", DimensionKey: "audit", RequestCount: n, SuccessCount: n}
	}
	old := metric(now.Add(-32*24*time.Hour), 100)
	fresh := metric(now, 2)
	// Seed a legacy marker and an already expired, safely aged-out new marker.
	_, err := db.Exec(`INSERT INTO metric_batches(instance_id,batch_id,created_at,expires_at) VALUES('audit','legacy',?,NULL),('audit','expired',?,?)`, old.BucketTime, old.BucketTime, now.Add(-time.Hour))
	require.NoError(t, err)
	// The additive migration also survives a missing ledger entry after DDL.
	_, err = db.Exec(`DELETE FROM schema_migrations WHERE version='141_metric_batch_retention.sql'`)
	require.NoError(t, err)
	require.NoError(t, ApplyDir(ctx, db, "../../migrations"))
	n, err := s.PruneBefore("metric_batches", now.Add(100*24*time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	// Expired retransmission is acknowledged without inserting a new key/data.
	require.NoError(t, s.ApplyMetricBatch("audit", "expired", []aggregator.Metric{old}))
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM metric_batches WHERE batch_id='expired'`).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM metric_batches WHERE batch_id='legacy' AND expires_at IS NULL`).Scan(&count))
	require.Equal(t, 1, count)
	// Concurrent duplicate writes and cleanup cannot remove a fresh key.
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.ApplyMetricBatch("audit", "fresh", []aggregator.Metric{old, fresh}) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); _, e := s.PruneBefore("metric_batches", now.Add(100*24*time.Hour)); errs <- e }()
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	for _, table := range []string{"metric_1m", "metric_5m"} {
		var total int64
		require.NoError(t, db.QueryRow("SELECT COALESCE(SUM(request_count),0) FROM "+table+" WHERE instance_id='audit'").Scan(&total))
		require.EqualValues(t, 2, total)
	}
	future := metric(now.Add(48*time.Hour), 3)
	require.NoError(t, s.ApplyMetricBatch("audit", "future", []aggregator.Metric{future}))
	var expiry time.Time
	require.NoError(t, db.QueryRow(`SELECT expires_at FROM metric_batches WHERE batch_id='future'`).Scan(&expiry))
	require.Equal(t, future.BucketTime.Add(aggregator.MetricBatchRetention), expiry)
	// Existing legacy IDs still deduplicate even after their age exceeds retention.
	require.NoError(t, s.ApplyMetricBatch("audit", "legacy", []aggregator.Metric{fresh}))
	var total int64
	require.NoError(t, db.QueryRow(`SELECT SUM(request_count) FROM metric_1m WHERE instance_id='audit'`).Scan(&total))
	require.EqualValues(t, 5, total)
}
