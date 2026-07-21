package storage

import (
	"os"
	"strings"
	"testing"
)

func TestOTPSMigrationAddsAdditiveFields(t *testing.T) {
	data, err := os.ReadFile("../../migrations/013_otps.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	for _, fragment := range []string{"ALTER TABLE metric_1m", "ALTER TABLE metric_5m", "otps_output_tokens BIGINT", "otps_duration_seconds DOUBLE"} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("migration missing %q", fragment)
		}
	}
}
