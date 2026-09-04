package mysqlstore

import (
	"context"
	"os"
	"testing"
	"time"

	"controltower/server/internal/aggregator"
)

// Second-resolution rate counters travel inside the ordinary metric batch.
// They must land only in channel_rate_seconds (never in metric_1m/metric_5m),
// share the batch de-duplication, and expire rows older than ten minutes.
func TestApplyMetricBatchRoutesChannelRateSeconds(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN to run the channel rate batch test")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	const instanceID = "rate-batch-test-instance"
	cleanup := func() {
		_, _ = db.Exec(`DELETE FROM channel_rate_seconds WHERE instance_id=?`, instanceID)
		_, _ = db.Exec(`DELETE FROM metric_1m WHERE instance_id=?`, instanceID)
		_, _ = db.Exec(`DELETE FROM metric_5m WHERE instance_id=?`, instanceID)
		_, _ = db.Exec(`DELETE FROM metric_batches WHERE instance_id=?`, instanceID)
	}
	cleanup()
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	stale := now.Add(-11 * time.Minute)
	if _, err := db.Exec(`INSERT INTO channel_rate_seconds(instance_id,channel_id,bucket_time,request_count,tokens) VALUES(?,7,?,1,1)`, instanceID, stale); err != nil {
		t.Fatalf("seed stale row: %v", err)
	}
	batch := []aggregator.Metric{
		{InstanceID: instanceID, BucketTime: now.Truncate(time.Minute), DimensionType: "instance_channel", DimensionKey: instanceID + ":channel:7", RequestCount: 3, SuccessCount: 3, TPM: 300},
		{InstanceID: instanceID, BucketTime: now, DimensionType: "channel_rate_second", DimensionKey: "0"},
		{InstanceID: instanceID, BucketTime: now.Add(-5 * time.Second), DimensionType: "channel_rate_second", DimensionKey: "7", RequestCount: 2, TPM: 200},
		{InstanceID: instanceID, BucketTime: now.Add(-5 * time.Second), DimensionType: "channel_rate_second", DimensionKey: "bogus", RequestCount: 9, TPM: 900},
	}
	store := New(db)
	for i := 0; i < 2; i++ { // second call is a retried duplicate
		if err := store.ApplyMetricBatch(instanceID, "rates-batch-1", batch); err != nil {
			t.Fatalf("apply batch #%d: %v", i+1, err)
		}
	}
	// The same second reported again under a new batch id accumulates.
	if err := store.ApplyMetricBatch(instanceID, "rates-batch-2", batch[2:3]); err != nil {
		t.Fatalf("apply batch 2: %v", err)
	}

	var seconds, tokens, marker, polluted1m, polluted5m, staleRows int64
	if err := db.QueryRow(`SELECT COALESCE(SUM(request_count),0),COALESCE(SUM(tokens),0) FROM channel_rate_seconds WHERE instance_id=? AND channel_id=7 AND bucket_time>?`, instanceID, stale).Scan(&seconds, &tokens); err != nil {
		t.Fatal(err)
	}
	if seconds != 4 || tokens != 400 {
		t.Fatalf("channel 7 seconds = %d/%d, want 4/400 (dedup on retry, accumulate on new batch)", seconds, tokens)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM channel_rate_seconds WHERE instance_id=? AND channel_id=0 AND bucket_time=?`, instanceID, now).Scan(&marker); err != nil {
		t.Fatal(err)
	}
	if marker != 1 {
		t.Fatalf("coverage marker rows = %d, want 1", marker)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM metric_1m WHERE instance_id=? AND dimension_type='channel_rate_second'`, instanceID).Scan(&polluted1m); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM metric_5m WHERE instance_id=? AND dimension_type='channel_rate_second'`, instanceID).Scan(&polluted5m); err != nil {
		t.Fatal(err)
	}
	if polluted1m != 0 || polluted5m != 0 {
		t.Fatalf("rate seconds leaked into minute tables: 1m=%d 5m=%d", polluted1m, polluted5m)
	}
	var regular int64
	if err := db.QueryRow(`SELECT COALESCE(SUM(request_count),0) FROM metric_1m WHERE instance_id=? AND dimension_type='instance_channel'`, instanceID).Scan(&regular); err != nil {
		t.Fatal(err)
	}
	if regular != 3 {
		t.Fatalf("regular minute metric = %d, want 3", regular)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM channel_rate_seconds WHERE instance_id=? AND bucket_time<=?`, instanceID, stale).Scan(&staleRows); err != nil {
		t.Fatal(err)
	}
	if staleRows != 0 {
		t.Fatalf("stale rate rows = %d, want expired", staleRows)
	}
}
