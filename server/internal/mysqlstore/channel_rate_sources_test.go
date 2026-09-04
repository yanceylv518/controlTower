package mysqlstore

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestChannelRateQueriesShareDurableSourceScope(t *testing.T) {
	for _, query := range []string{channelRateCoverageSQL, rollingChannelRatesSQL} {
		if !strings.Contains(query, channelRateSourcesSQL) {
			t.Fatal("coverage and totals must share the source scope")
		}
	}
	for _, fragment := range []string{"i.enabled=1", "a.source_latest_log_id>0", "a.last_log_id>0", "o.last_log_id>0"} {
		if !strings.Contains(channelRateSourcesSQL, fragment) {
			t.Fatalf("missing source evidence: %s", fragment)
		}
	}
	if strings.Contains(channelRateSourcesSQL, "last_seen_at") || strings.Contains(channelRateSourcesSQL, "channel_rate_seconds") {
		t.Fatal("expired reports must not remove a real collector from required sources")
	}
}

func TestRollingRatesIgnoreMetricsOnlyButRequireEveryCollector(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN to run source coverage integration test")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	const site = "rate-source-scope-test"
	ids := []string{site + "-logs", site + "-metrics", site + "-logs2"}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	cleanup := func() {
		for _, id := range ids {
			for _, table := range []string{"channel_rate_seconds", "log_offsets", "agents"} {
				_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", id)
			}
			_, _ = db.Exec("DELETE FROM instances WHERE id=?", id)
		}
	}
	cleanup()
	defer cleanup()
	now := time.Now().UTC().Truncate(time.Second)
	for _, id := range ids {
		exec(`INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, id, site, id, now, now)
	}
	// The first collector is known through a durable cursor. The other two
	// instances initially have no log collection evidence and no rate markers.
	exec(`INSERT INTO log_offsets(instance_id,last_log_id,updated_at) VALUES(?,100,?)`, ids[0], now)
	exec(`INSERT INTO channel_rate_seconds(instance_id,channel_id,bucket_time,request_count,tokens) VALUES(?,0,?,0,0),(?,7,?,5,500)`, ids[0], now, ids[0], now.Add(-20*time.Second))
	store := New(db)
	rates, err := store.QueryRollingChannelRates(site, now)
	if err != nil || len(rates) != 1 || rates[0].RequestCount != 5 || rates[0].TPM != 500 {
		t.Fatalf("metrics-only instance blocked valid load: %+v, %v", rates, err)
	}
	// A metrics-only rc92 Agent can send a fresh marker too; it must not hide
	// a missing collector. Recognize the second collector from Agent telemetry.
	exec(`INSERT INTO channel_rate_seconds(instance_id,channel_id,bucket_time) VALUES(?,0,?)`, ids[1], now)
	exec(`INSERT INTO agents(id,instance_id,version,last_seen_at,last_sequence,last_log_id,source_latest_log_id,backlog_estimate,status,report_delay_ms) VALUES(?,?,'old',?,1,0,200,0,'online',0)`, ids[2], ids[2], now)
	if _, err := store.QueryRollingChannelRates(site, now); err == nil {
		t.Fatal("uncovered second collector must block incomplete totals")
	}
	exec(`INSERT INTO channel_rate_seconds(instance_id,channel_id,bucket_time,request_count,tokens) VALUES(?,0,?,0,0),(?,7,?,3,300)`, ids[2], now.Add(-10*time.Second), ids[2], now.Add(-20*time.Second))
	// Faster source has already reported a tail that the slower source has not
	// covered. Both sources must be summed over the same earlier window.
	exec(`INSERT INTO channel_rate_seconds(instance_id,channel_id,bucket_time,request_count,tokens) VALUES(?,7,?,99,9900)`, ids[0], now.Add(-5*time.Second))
	_, asOf, err := store.QueryCurrentChannelRateSnapshot(site, now)
	if err != nil || !asOf.Equal(now.Add(-10*time.Second)) {
		t.Fatalf("common watermark = %s, err=%v", asOf, err)
	}
	rates, err = store.QueryRollingChannelRates(site, now)
	if err != nil || len(rates) != 1 || rates[0].RequestCount != 8 {
		t.Fatalf("two-source totals: %+v %v", rates, err)
	}
	exec(`DELETE FROM channel_rate_seconds WHERE instance_id=?`, ids[0])
	if _, err := store.QueryRollingChannelRates(site, now); err == nil {
		t.Fatal("collector with expired/deleted buckets must remain required")
	}
	exec(`UPDATE instances SET enabled=0 WHERE id=?`, ids[0])
	rates, err = store.QueryRollingChannelRates(site, now)
	if err != nil || len(rates) != 1 || rates[0].RequestCount != 3 {
		t.Fatalf("disabled source should be excluded: %+v %v", rates, err)
	}
	exec(`DELETE FROM channel_rate_seconds WHERE channel_id>0 AND instance_id=?`, ids[2])
	rates, err = store.QueryRollingChannelRates(site, now)
	if err != nil || len(rates) != 0 {
		t.Fatalf("fresh quiet source should yield zero load: %+v %v", rates, err)
	}
	exec(`UPDATE instances SET enabled=0 WHERE id=?`, ids[2])
	if _, err := store.QueryRollingChannelRates(site, now); err == nil {
		t.Fatal("metrics-only site must not claim valid zero channel traffic")
	}
}
