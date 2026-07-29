package mysqlstore

import (
	"strings"
	"testing"
)

func TestLatestChannelsSQLAggregatesSnapshotsOnce(t *testing.T) {
	for _, fragment := range []string{
		"JOIN (",
		"MAX(captured_at) AS captured_at",
		"WHERE instance_id=?",
		"GROUP BY channel_id",
		"latest.channel_id=c.channel_id",
		"latest.captured_at=c.captured_at",
	} {
		if !strings.Contains(latestChannelsSQL, fragment) {
			t.Fatalf("latest channel query missing %q: %s", fragment, latestChannelsSQL)
		}
	}
	if strings.Contains(latestChannelsSQL, "c2.instance_id=c.instance_id") {
		t.Fatalf("latest channel query must not use the per-row correlated subquery: %s", latestChannelsSQL)
	}
}

func TestTuningChannelMetricsSQLExtractsChannelIDFromDimensionKey(t *testing.T) {
	if !strings.Contains(tuningChannelMetricsSQL, "SUBSTRING_INDEX(dimension_key,':',-1)") {
		t.Fatalf("channel metrics query must split the channel id out of '<instance>:channel:<id>': %s", tuningChannelMetricsSQL)
	}
	if strings.Contains(tuningChannelMetricsSQL, "CAST(dimension_key AS SIGNED)") {
		t.Fatalf("casting the whole dimension key yields 0 for every row: %s", tuningChannelMetricsSQL)
	}
}

func TestTuningP95BucketsSQLBuildsFullDimensionKey(t *testing.T) {
	if !strings.Contains(tuningP95BucketsSQL, "dimension_key=CONCAT(?,':channel:',?)") {
		t.Fatalf("baseline query must build the full '<instance>:channel:<id>' key: %s", tuningP95BucketsSQL)
	}
	if strings.Contains(tuningP95BucketsSQL, "dimension_key=? ") {
		t.Fatalf("binding the bare channel id never matches any bucket: %s", tuningP95BucketsSQL)
	}
}
