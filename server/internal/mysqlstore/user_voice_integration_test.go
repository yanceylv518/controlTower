package mysqlstore

import (
	"context"
	"controltower/server/internal/aggregator"
	"controltower/server/internal/voicealert"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUserVoiceIntegration(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN for voice migration, atomic cooldown and coverage tests")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	store := New(db)
	voice := voicealert.Store{DB: db}
	site := fmt.Sprintf("voice-test-%d", time.Now().UnixNano())
	now := time.Now().UTC().Truncate(time.Second)
	cleanup := func() {
		for _, table := range []string{"metric_batches", "metric_1m", "metric_5m", "user_rate_seconds", "log_offsets"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site)
		}
		_, _ = db.Exec("DELETE FROM instances WHERE id=?", site)
		_, _ = db.Exec("DELETE FROM voice_alert_calls WHERE site_id=?", site)
	}
	defer cleanup()
	if _, err := db.Exec(`INSERT INTO instances(id,name,site_id,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, site, site, site, now, now); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateLogOffset(site, 1); err != nil {
		t.Fatal(err)
	}
	batch := []aggregator.Metric{}
	for i := 0; i <= 12; i++ {
		batch = append(batch, aggregator.Metric{BucketTime: now.Add(time.Duration(i-12) * 30 * time.Second), DimensionType: "user_rate_second", DimensionKey: "0"})
	}
	batch = append(batch, aggregator.Metric{BucketTime: now.Add(-time.Second), DimensionType: "user_rate_second", DimensionKey: "7", TPM: 20000001, RequestCount: 1})
	for i := 0; i < 2; i++ {
		if err := store.ApplyMetricBatch(site, "voice-batch", batch); err != nil {
			t.Fatal(err)
		}
	}
	target := voicealert.Target{Site: site, UserID: 7, Phone: "13800000000"}
	values, end, err := voice.Snapshot(ctx, target, now)
	if err != nil {
		t.Fatal(err)
	}
	if values[10] != 20000001 || values[0] != 0 {
		t.Fatal("dedup/window", values)
	}
	for _, table := range []string{"metric_1m", "metric_5m"} {
		var n int
		if err = db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE instance_id=?", site).Scan(&n); err != nil || n != 0 {
			t.Fatal("special dimension leaked", table, n, err)
		}
	}
	// Use a test-specific recipient so parallel test runs cannot share quotas.
	target.Phone = fmt.Sprintf("test-%d", time.Now().UnixNano())
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, e := voice.Claim(ctx, target, end, 0, 20000001, "上涨", now)
			if e != nil {
				t.Error(e)
			}
			if id != "" {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal("concurrent calls", winners.Load())
	}
	// Finishing an accepted call extends cooldown from its outcome time.
	var firstID string
	if err = db.QueryRow("SELECT id FROM voice_alert_calls WHERE site_id=?", site).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if err = voice.Finish(ctx, firstID, voicealert.Result{Status: "accepted", Code: "OK", CallID: "fake-call", RequestID: "fake-request"}, now.Add(20*time.Second)); err != nil {
		t.Fatal(err)
	}
	// A second subscribed phone for the same customer is claimed independently.
	copyTarget := target
	copyTarget.Phone += "other"
	id, e := (voicealert.Store{DB: db}).Claim(ctx, copyTarget, end, 0, 20000001, "上涨", now)
	if e != nil || id == "" {
		t.Fatal("second recipient suppressed", id, e)
	}
	if e = voice.Finish(ctx, id, voicealert.Result{Status: "accepted", Code: "OK"}, now.Add(20*time.Second)); e != nil {
		t.Fatal(e)
	}
	// Different customer sharing a phone respects global recipient rate limits.
	copyTarget = target
	copyTarget.UserID = 8
	id, e = voice.Claim(ctx, copyTarget, end, 0, 20000001, "上涨", now.Add(20*time.Second))
	if e != nil || id != "" {
		t.Fatal("phone minute limit", id, e)
	}
	for i := 1; i <= 4; i++ {
		copyTarget.UserID = int64(8 + i)
		id, e = voice.Claim(ctx, copyTarget, end, 0, 20000001, "上涨", now.Add(time.Duration(i)*2*time.Minute))
		if e != nil || id == "" {
			t.Fatal("phone allowed slots", id, e)
		}
	}
	copyTarget.UserID = 99
	id, e = voice.Claim(ctx, copyTarget, end, 0, 20000001, "上涨", now.Add(11*time.Minute))
	if e != nil || id != "" {
		t.Fatal("phone hourly limit", id, e)
	}
	// The second recipient has its own customer cooldown.
	copyTarget = target
	copyTarget.Phone += "other"
	id, e = voice.Claim(ctx, copyTarget, end, 0, 20000001, "上涨", now.Add(10*time.Minute+19*time.Second))
	if e != nil || id != "" {
		t.Fatal("early expiry", id, e)
	}
	id, e = voice.Claim(ctx, copyTarget, end, 0, 20000001, "上涨", now.Add(10*time.Minute+21*time.Second))
	if e != nil || id == "" {
		t.Fatal("cooldown never expires", id, e)
	}
	// Fill three later hourly groups on the first recipient: 20 attempts total.
	copyTarget = target
	for hour := 1; hour <= 3; hour++ {
		for j := 0; j < 5; j++ {
			copyTarget.UserID = int64(100 + hour*10 + j)
			id, e = voice.Claim(ctx, copyTarget, end, 0, 20000001, "上涨", now.Add(time.Duration(hour)*2*time.Hour+time.Duration(j)*2*time.Minute))
			if e != nil || id == "" {
				t.Fatal("daily allowed slots", id, e)
			}
		}
	}
	copyTarget.UserID = 999
	id, e = voice.Claim(ctx, copyTarget, end, 0, 20000001, "上涨", now.Add(8*time.Hour))
	if e != nil || id != "" {
		t.Fatal("daily limit ignored", id, e)
	}
	// A dropped report must remain invalid even when adjacent marks are only
	// 30 seconds apart, and even after another report reaches the watermark.
	if _, err = db.Exec(`UPDATE user_rate_seconds SET request_count=1 WHERE instance_id=? AND user_id=0 AND bucket_time=?`, site, now.Add(-30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = voice.Snapshot(ctx, target, now); err != voicealert.ErrCoverage {
		t.Fatal("invalid coverage accepted", err)
	}
	if _, err = db.Exec(`UPDATE user_rate_seconds SET request_count=0 WHERE instance_id=? AND user_id=0`, site); err != nil {
		t.Fatal(err)
	}
	// A missing marker segment must suppress detection, not turn into zero TPM.
	if _, err = db.Exec(`DELETE FROM user_rate_seconds WHERE instance_id=? AND user_id=0 AND bucket_time>? AND bucket_time<?`, site, now.Add(-4*time.Minute), now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = voice.Snapshot(ctx, target, now); err != voicealert.ErrCoverage {
		t.Fatal("gap accepted", err)
	}
}
