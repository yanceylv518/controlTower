package mysqlstore

import (
	"context"
	"os"
	"testing"
	"time"

	"controltower/server/internal/tuning"
)

// The newest closed minute bucket is still being filled by Agent reports for
// up to a poll interval after it closes. Reading it would undercount the rate.
func TestQueryCurrentChannelRatesSkipsSettlingBucket(t *testing.T) {
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
		_, _ = db.Exec(`DELETE FROM metric_1m WHERE instance_id=?`, instanceID)
		_, _ = db.Exec(`DELETE FROM instances WHERE id=?`, instanceID)
	}
	cleanup()
	defer cleanup()
	now := time.Date(2026, 9, 2, 10, 5, 10, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO instances(id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?)`, instanceID, "rates", "test", "local", "http://127.0.0.1", now, now); err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	insert := func(bucket time.Time, requests, tpm int64) {
		if _, err := db.Exec(`INSERT INTO metric_1m(instance_id,bucket_time,dimension_type,dimension_key,request_count,success_count,error_count,tpm,prompt_tokens,completion_tokens,quota,updated_at) VALUES(?,?,'instance_channel',?,?,?,0,?,0,0,0,?)`, instanceID, bucket, instanceID+":7", requests, requests, tpm, now); err != nil {
			t.Fatalf("insert metric: %v", err)
		}
	}
	minute := now.Truncate(time.Minute)
	insert(minute.Add(-3*time.Minute), 30, 3000) // 10:02, settled
	insert(minute.Add(-2*time.Minute), 60, 6000) // 10:03, closed 70s ago: settled
	insert(minute.Add(-time.Minute), 10, 1000)   // 10:04, closed 10s ago: still filling
	insert(minute, 5, 500)                       // 10:05, current minute

	got, err := New(db).QueryCurrentChannelRates(instanceID, now)
	if err != nil {
		t.Fatalf("query rates: %v", err)
	}
	want := []tuning.ChannelMetric{{ChannelID: 7, RequestCount: 60, TPM: 6000}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("rates = %#v, want %#v", got, want)
	}

	// 10:04 closed at 10:05:00; by 10:05:35 it has settled and becomes current.
	got, err = New(db).QueryCurrentChannelRates(instanceID, minute.Add(35*time.Second))
	if err != nil {
		t.Fatalf("query rates after settle: %v", err)
	}
	if len(got) != 1 || got[0].RequestCount != 10 {
		t.Fatalf("rates after settle = %#v, want request_count 10", got)
	}

	// An idle site (no bucket in the last five settled minutes) reads as zero.
	got, err = New(db).QueryCurrentChannelRates(instanceID, now.Add(15*time.Minute))
	if err != nil {
		t.Fatalf("query rates idle: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("idle rates = %#v, want none", got)
	}
}
