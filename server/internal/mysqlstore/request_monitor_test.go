package mysqlstore

import (
	"context"
	"controltower/internal/latencyhist"
	"controltower/server/internal/aggregator"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRequestMonitorMySQL(t *testing.T) {
	dsn := os.Getenv("CT_ALB_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable CT_ALB_TEST_DSN")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name string
	if err = db.QueryRow("SELECT DATABASE()").Scan(&name); err != nil || !strings.HasPrefix(name, "ct_request_monitor_test_") {
		t.Fatal("requires isolated ct_request_monitor_test_ database")
	}
	ctx := context.Background()
	if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM metric_1m").Scan(&count); err != nil || count != 0 {
		t.Fatal("requires empty metric table")
	}
	from := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	to := from.Add(5 * time.Minute)
	for _, id := range []string{"req-test-a", "req-test-b"} {
		if _, err = db.Exec("INSERT INTO instances(id,name,site_id,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)", id, id, id, from, from); err != nil {
			t.Fatal(err)
		}
	}
	n := int64(20)
	hist := latencyhist.BucketsV2{}
	hist[10] = 20
	sample := func(id, dim string, at time.Time) aggregator.Metric {
		return aggregator.Metric{InstanceID: id, DimensionType: dim, DimensionKey: id + ":channel:1", BucketTime: at, RequestCount: 100, TTFTCount: &n, TTFTBuckets: &hist}
	}
	s := New(db)
	rows := []aggregator.Metric{
		sample("req-test-a", "instance_channel", from),
		sample("req-test-b", "instance_channel", from.Add(4*time.Minute)),
		sample("req-test-a", "instance_channel", from.Add(-time.Minute)),
		sample("req-test-a", "instance_channel", to),
		sample("req-test-b", "instance_user", from),
	}
	if err = s.Upsert1m(rows); err != nil {
		t.Fatal(err)
	}
	got, err := s.RequestMonitorMetrics(ctx, from, to)
	if err != nil || len(got) != 2 {
		t.Fatalf("rows=%d error=%v", len(got), err)
	}
	for _, m := range got {
		if m.TTFTBuckets == nil || (*m.TTFTBuckets)[10] != 20 || m.TTFTCount == nil || *m.TTFTCount != 20 {
			t.Fatal("histogram lost in storage")
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.RequestMonitorMetrics(cancelled, from, to); err == nil {
		t.Fatal("ignored cancellation")
	}
}
