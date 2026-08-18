package storage

import (
	"os"
	"strings"
	"testing"
)

func TestBillingUpstreamMigration045IsSingleAdditiveStatement(t *testing.T) {
	data, err := os.ReadFile("../../migrations/045_billing_upstream_channels.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(strings.TrimSpace(string(data)))
	if strings.Count(sql, ";") != 1 || !strings.Contains(sql, "create table if not exists billing_upstream_channels") || !strings.Contains(sql, "primary key(instance_id,channel_id)") || !strings.Contains(sql, "key idx_upstream_fp(instance_id,upstream_fp)") {
		t.Fatalf("migration=%s", sql)
	}
	for _, bad := range []string{"drop table", "delete from", "truncate table", "alter table"} {
		if strings.Contains(sql, bad) {
			t.Fatalf("migration contains %q", bad)
		}
	}
}
