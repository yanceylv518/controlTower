package storage

import (
	"os"
	"strings"
	"testing"
)

func TestTuningConfirmMigration(t *testing.T) {
	data, err := os.ReadFile("../../migrations/016_tuning_confirm.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	for _, fragment := range []string{
		"ADD COLUMN acted_by",
		"ADD COLUMN acted_at",
		"idx_tuning_status_created",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("migration missing %q", fragment)
		}
	}
}
