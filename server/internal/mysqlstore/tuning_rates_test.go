package mysqlstore

import (
	"context"
	"os"
	"testing"
	"time"

	"controltower/server/internal/tuning"
)

// Rolling rates include current-minute reports and expire by event timestamp,
// rather than waiting for minute buckets to settle.
func TestQueryCurrentChannelRatesRollingWindow(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN to run the current channel rates test")
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
	const instanceID = "rates-test-instance"
	cleanup := func() {
		_, _ = db.Exec(`DELETE FROM channel_rate_seconds WHERE instance_id=?`, instanceID)
		_, _ = db.Exec(`DELETE FROM instances WHERE id=?`, instanceID)
	}
	cleanup()
	defer cleanup()
	now := time.Date(2026, 9, 2, 10, 5, 10, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO instances(id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?)`, instanceID, "rates", "test", "local", "http://127.0.0.1", now, now); err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	insert := func(bucket time.Time, requests, tpm int64) {
		if _, err := db.Exec(`INSERT INTO channel_rate_seconds(instance_id,channel_id,bucket_time,request_count,tokens) VALUES(?,7,?,?,?)`, instanceID, bucket, requests, tpm); err != nil {
			t.Fatalf("insert metric: %v", err)
		}
	}
	minute := now.Truncate(time.Minute)
	if _, err := db.Exec(`INSERT INTO channel_rate_seconds(instance_id,channel_id,bucket_time) VALUES(?,0,?)`, instanceID, now); err != nil {
		t.Fatal(err)
	}
	insert(minute.Add(-3*time.Minute), 30, 3000) // 10:02, settled
	insert(minute.Add(-2*time.Minute), 60, 6000) // 10:03, closed 70s ago: settled
	insert(minute.Add(-time.Minute), 10, 1000)   // 10:04, closed 10s ago: still filling
	insert(minute, 5, 500)                       // 10:05, current minute

	got, err := New(db).QueryCurrentChannelRates(instanceID, now)
	if err != nil {
		t.Fatalf("query rates: %v", err)
	}
	want := []tuning.ChannelMetric{{ChannelID: 7, RequestCount: 5, TPM: 500}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("rates = %#v, want %#v", got, want)
	}

	// No minute transition is required for the newest data to appear.
	got, err = New(db).QueryCurrentChannelRates(instanceID, minute.Add(35*time.Second))
	if err != nil {
		t.Fatalf("query rates after settle: %v", err)
	}
	if len(got) != 1 || got[0].RequestCount != 5 {
		t.Fatalf("rolling rates = %#v, want request_count 5", got)
	}

	// Missing coverage is unavailable, not a fabricated zero load.
	got, err = New(db).QueryCurrentChannelRates(instanceID, now.Add(15*time.Minute))
	if err == nil {
		t.Fatalf("expected stale coverage error, got %#v", got)
	}
}
