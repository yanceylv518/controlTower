package mysqlstore

import (
	"context"
	"controltower/internal/latencyhist"
	"controltower/server/internal/aggregator"
	"controltower/server/internal/storage"
	"errors"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRequestRulesMySQLIsolated(t *testing.T) {
	raw := os.Getenv("CT_REQUEST_RULES_ADMIN_DSN")
	if raw == "" {
		t.Skip("requires local MySQL test administrator")
	}
	cfg, err := mysql.ParseDSN(raw)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	if !strings.HasPrefix(cfg.Addr, "127.0.0.1:") && !strings.HasPrefix(cfg.Addr, "localhost:") {
		t.Fatal("local database required")
	}
	cfg.DBName = ""
	admin, err := Open(cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("ct_request_rules_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec("DROP DATABASE " + name)
	cfg.DBName = name
	db, err := Open(cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	c, err := s.LoadRequestMonitorRules(ctx)
	if err != nil || c != storage.DefaultRequestMonitorRules() {
		t.Fatal(c, err)
	}
	c.SiteID = "site-a"
	c.WindowMinutes = 10
	c.TTFTSeconds = 15
	c.ErrorPercent = 2.5
	if err = s.SaveRequestMonitorRules(ctx, c, "test"); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveRequestMonitorRules(ctx, c, "stale"); !errors.Is(err, storage.ErrRequestRulesConflict) {
		t.Fatal("missing insert conflict", err)
	}
	reloaded, err := New(db).LoadRequestMonitorRules(ctx)
	if err != nil || reloaded.Version != 1 || reloaded.WindowMinutes != 10 || reloaded.ErrorPercent != 2.5 {
		t.Fatal(reloaded, err)
	}
	reloaded.MinRequests = 200
	if err = s.SaveRequestMonitorRules(ctx, reloaded, "test"); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveRequestMonitorRules(ctx, reloaded, "stale"); !errors.Is(err, storage.ErrRequestRulesConflict) {
		t.Fatal("missing update conflict", err)
	}
	from := time.Now().UTC().Truncate(time.Minute)
	b := latencyhist.BucketsV2{}
	b[10] = 100
	n := int64(100)
	for _, id := range []string{"a", "b"} {
		_, err = db.Exec("INSERT INTO instances(id,name,site_id,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)", id, id, id, from, from)
		if err != nil {
			t.Fatal(err)
		}
		err = s.Upsert1m([]aggregator.Metric{{InstanceID: id, DimensionType: "instance_channel", DimensionKey: id + ":channel:1", BucketTime: from, RequestCount: 100, ErrorCount: 10, TTFTCount: &n, TTFTBuckets: &b, LatencyBucketsV2: &b}})
		if err != nil {
			t.Fatal(err)
		}
	}
	metrics, err := s.RequestMonitorMetrics(ctx, from, from.Add(time.Minute), "a")
	if err != nil || len(metrics) != 1 || metrics[0].InstanceID != "a" || metrics[0].ErrorCount != 10 || metrics[0].LatencyBucketsV2 == nil {
		t.Fatal("scoped metrics mismatch", err)
	}
	metrics, err = s.RequestMonitorMetrics(ctx, from, from.Add(time.Minute), "a", "b")
	if err != nil || len(metrics) != 2 {
		t.Fatal("multi-node scope mismatch", err)
	}
}
