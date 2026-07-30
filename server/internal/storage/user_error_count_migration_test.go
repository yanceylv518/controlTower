package storage

import (
	"os"
	"strings"
	"testing"
)

func TestUserErrorCountMigrationIsAdditive(t *testing.T) {
	data, err := os.ReadFile("../../migrations/017_user_error_count.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(data))
	for _, table := range []string{"metric_1m", "metric_5m"} {
		if !strings.Contains(sql, "alter table "+table) ||
			!strings.Contains(sql, "add column user_error_count bigint not null default 0") {
			t.Fatalf("migration does not add user_error_count to %s", table)
		}
	}
	for _, forbidden := range []string{"drop table", "drop column", "rename", " change ", " modify "} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("migration must remain additive; found %q", forbidden)
		}
	}
}
