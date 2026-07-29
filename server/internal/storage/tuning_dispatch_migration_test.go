package storage

import (
	"os"
	"strings"
	"testing"
)

func TestTuningDispatchMigrationIsForwardOnly(t *testing.T) {
	data, err := os.ReadFile("../../migrations/015_tuning_dispatch_states.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToUpper(string(data))
	for _, required := range []string{"CREATE TABLE IF NOT EXISTS TUNING_DISPATCH_STATES", "PRIMARY KEY (INSTANCE_ID, CHANNEL_ID)"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
	for _, forbidden := range []string{"DROP ", "ALTER ", "RENAME ", "MODIFY "} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("migration must be forward-only; found %q", forbidden)
		}
	}
}
