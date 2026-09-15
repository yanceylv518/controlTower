package mysqlstore

import (
	"context"
	"controltower/internal/latencyhist"
	"controltower/internal/speedstats"
	"controltower/server/internal/aggregator"
	"controltower/server/internal/tuning"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestSpeedTTFTMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires local test DB")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := ApplyDir(context.Background(), db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	now := time.Now().UTC().Truncate(time.Minute)
	site := fmt.Sprintf("speed-test-%d", now.UnixNano())
	if _, err := db.Exec(`INSERT INTO instances(id,name,site_id,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, site, site, site, now, now); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"metric_batches", "metric_1m", "metric_5m", "tuning_continuous_states"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site)
		}
		_, _ = db.Exec("DELETE FROM instances WHERE id=?", site)
	}()
	count := int64(3)
	public := latencyhist.BucketsV2{}
	public[9] = 3
	stats := speedstats.New()
	stats.Buckets[2] = 1
	stats.RetryCount = 1
	stats.UnknownCount = 1
	m := aggregator.Metric{InstanceID: site, BucketTime: now, DimensionType: "instance_channel", DimensionKey: site + ":channel:7", RequestCount: 3, SuccessCount: 3, TTFTCount: &count, TTFTBuckets: &public, SpeedTTFT: stats}
	for _, id := range []string{"a", "a", "b"} {
		if err := s.ApplyMetricBatch(site, site+id, []aggregator.Metric{m}); err != nil {
			t.Fatal(err)
		}
	}
	m.SpeedTTFT = nil
	if err := s.ApplyMetricBatch(site, site+"legacy", []aggregator.Metric{m}); err != nil {
		t.Fatal(err)
	}
	for _, window := range []string{"1m", "5m"} {
		items, err := s.QueryMetricHistory(window, m.DimensionType, m.DimensionKey, now.Add(-5*time.Minute))
		if err != nil || len(items) != 1 {
			t.Fatalf("%s %+v %v", window, items, err)
		}
		v := items[0]
		if v.RequestCount != 9 || *v.TTFTCount != 9 || v.SpeedTTFT.Samples() != 2 || v.SpeedTTFT.RetryCount != 2 {
			t.Fatalf("%s %+v", window, v)
		}
	}
	metrics, err := s.QueryMetrics(site, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil || len(metrics) != 1 {
		t.Fatalf("%+v %v", metrics, err)
	}
	v := metrics[0]
	if v.SpeedSamples != 2 || v.SpeedRetries != 2 || v.SpeedUnknown != 2 || v.SpeedLegacy != 3 || v.TTFTP50 <= v.SpeedTTFTP50 {
		t.Fatalf("query %+v", v)
	}
	state := tuning.ContinuousState{InstanceID: site, ChannelID: 7, ModelName: "m", KSpeed: 1.2, SpeedStatsVersion: 1, SpeedSamples: 2, SpeedRetries: 2, SpeedUnknown: 2, SpeedLegacy: 3, Phase: "normal", UpdatedAt: now}
	if err := s.PutContinuousState(state); err != nil {
		t.Fatal(err)
	}
	states, err := s.ListContinuousStates(site)
	if err != nil || len(states) != 1 || states[0].SpeedSamples != 2 || states[0].SpeedLegacy != 3 || states[0].KSpeed != 1.2 {
		t.Fatalf("state %+v %v", states, err)
	}
}
