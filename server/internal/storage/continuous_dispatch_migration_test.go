package storage

import (
	"os"
	"strings"
	"testing"
)

func TestContinuousDispatchMigrationIsForwardOnly(t *testing.T) {
	base, err := os.ReadFile("../../migrations/019_continuous_dispatch_states.sql")
	if err != nil {
		t.Fatal(err)
	}
	b3, err := os.ReadFile("../../migrations/020_continuous_circuit_breaker.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(base) + "\n" + string(b3))
	for _, required := range []string{
		"create table if not exists tuning_continuous_states",
		"add column phase",
		"primary key (instance_id, channel_id)",
		"idx_tuning_continuous_instance_model",
		"last_write_at datetime(6)",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
	for _, forbidden := range []string{"drop table", "truncate table", "delete from"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("migration must be forward-only; found %q", forbidden)
		}
	}
}
