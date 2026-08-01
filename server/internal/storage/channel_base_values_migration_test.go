package storage

import (
	"os"
	"strings"
	"testing"
)

func TestChannelBaseValuesMigrationIsForwardOnly(t *testing.T) {
	data, err := os.ReadFile("../../migrations/018_channel_base_values.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(data))
	for _, required := range []string{
		"create table if not exists channel_base_values",
		"primary key (instance_id, channel_id)",
		"idx_channel_base_values_instance_model",
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
