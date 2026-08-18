package storage

import (
	"os"
	"strings"
	"testing"
)

func TestBillingTokenMigration046IsSingleNonDestructiveStatement(t *testing.T) {
	b, e := os.ReadFile("../../migrations/046_billing_token_daily_versions.sql")
	if e != nil {
		t.Fatal(e)
	}
	sql := string(b)
	if strings.Count(sql, ";") != 1 {
		t.Fatalf("migration must contain one statement: %q", sql)
	}
	lower := strings.ToLower(sql)
	for _, want := range []string{"create table if not exists billing_token_daily_versions", "primary key(job_id,user_id,token_id,model_name,group_name,tier_from,day)", "key idx_token_versions_report(instance_id,day,user_id,token_id)"} {
		if !strings.Contains(lower, want) {
			t.Fatalf("missing %q", want)
		}
	}
	for _, bad := range []string{"alter table", "drop ", "delete ", "truncate "} {
		if strings.Contains(lower, bad) {
			t.Fatalf("destructive migration contains %q", bad)
		}
	}
}
