package mysqlstore

import (
	"os"
	"strings"
	"testing"
)

func TestLatestChannelsSQLAggregatesSnapshotsOnce(t *testing.T) {
	for _, fragment := range []string{
		"JOIN (",
		"MAX(captured_at) AS captured_at",
		"CASE WHEN i2.site_id='' THEN i2.id ELSE i2.site_id END=?",
		"GROUP BY channel_id",
		"latest.channel_id=c.channel_id",
		"latest.captured_at=c.captured_at",
	} {
		if !strings.Contains(latestChannelsSQL, fragment) {
			t.Fatalf("latest channel query missing %q: %s", fragment, latestChannelsSQL)
		}
	}
	if !strings.Contains(latestChannelsSQL, "FROM channel_current") {
		t.Fatalf("latest channel query must read current state: %s", latestChannelsSQL)
	}
	if strings.Contains(latestChannelsSQL, "channel_snapshots") {
		t.Fatalf("latest channel query must not read historical snapshots: %s", latestChannelsSQL)
	}
	if strings.Contains(latestChannelsSQL, "c2.instance_id=c.instance_id") {
		t.Fatalf("latest channel query must not use the per-row correlated subquery: %s", latestChannelsSQL)
	}
}

func TestTuningChannelMetricsSQLExtractsChannelIDFromDimensionKey(t *testing.T) {
	query := tuningChannelMetricsSQL()
	if !strings.Contains(query, "SUBSTRING_INDEX(dimension_key,':',-1)") {
		t.Fatalf("channel metrics query must split the channel id out of '<instance>:channel:<id>': %s", query)
	}
	if strings.Contains(query, "CAST(dimension_key AS SIGNED)") {
		t.Fatalf("casting the whole dimension key yields 0 for every row: %s", query)
	}
	for _, column := range ttft2BucketColumns {
		if !strings.Contains(query, "SUM(COALESCE("+column+",0))") {
			t.Fatalf("channel metrics query must merge TTFT histogram column %s: %s", column, query)
		}
	}
}

func TestTuningRecentChannelBucketsSQLUsesNewestNonEmptyBuckets(t *testing.T) {
	for _, fragment := range []string{
		"SUBSTRING_INDEX(dimension_key,':',-1)",
		"bucket_time>=?",
		"request_count>0",
		"GROUP BY bucket_time",
		"ORDER BY bucket_time DESC",
		"LIMIT ?",
	} {
		if !strings.Contains(tuningRecentChannelBucketsSQL, fragment) {
			t.Fatalf("recent channel bucket query missing %q: %s", fragment, tuningRecentChannelBucketsSQL)
		}
	}
}

func TestTuningRecentChannelBucketsBySiteSQLScansWindowOnce(t *testing.T) {
	for _, fragment := range []string{
		"FORCE INDEX (idx_metric_1m_bucket_dimension)",
		"SUBSTRING_INDEX(dimension_key,':',-1)",
		"bucket_time>=?",
		"request_count>0",
		"GROUP BY CAST(SUBSTRING_INDEX(dimension_key,':',-1) AS SIGNED),bucket_time",
	} {
		if !strings.Contains(tuningRecentChannelBucketsBySiteSQL, fragment) {
			t.Fatalf("batch recent channel bucket query missing %q: %s", fragment, tuningRecentChannelBucketsBySiteSQL)
		}
	}
	if strings.Contains(tuningRecentChannelBucketsBySiteSQL, "LIMIT ?") {
		t.Fatalf("site batch must return every channel bucket in the bounded window: %s", tuningRecentChannelBucketsBySiteSQL)
	}
	if strings.Contains(tuningRecentChannelBucketsBySiteSQL, "ORDER BY") {
		t.Fatalf("site batch must sort its small result in Go instead of making MySQL filesort the metric range: %s", tuningRecentChannelBucketsBySiteSQL)
	}
}

func TestChannelBaseValuesPersistenceContracts(t *testing.T) {
	source, err := os.ReadFile("tuning.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, fragment := range []string{
		"ON DUPLICATE KEY UPDATE model_name=VALUES(model_name)",
		`"tuning.base_update"`,
		"before_summary,after_summary",
		"len(c.Models) != 1",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("base-value persistence missing %q", fragment)
		}
	}
}
