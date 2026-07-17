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
