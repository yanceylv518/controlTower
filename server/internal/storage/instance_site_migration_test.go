package storage

import (
	"os"
	"strings"
	"testing"
)

func TestInstanceSiteMigrationIsAdditive(t *testing.T) {
	data, err := os.ReadFile("../../migrations/014_instance_site.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToUpper(string(data))
	if !strings.Contains(sql, "ALTER TABLE INSTANCES") ||
		!strings.Contains(sql, "ADD COLUMN SITE_ID VARCHAR(64) NOT NULL DEFAULT ''") {
		t.Fatalf("unexpected migration: %s", data)
	}
	for _, forbidden := range []string{"CREATE TABLE", "DROP TABLE", "RENAME TABLE", "CHANGE COLUMN", "MODIFY COLUMN"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("migration must not rebuild instances table: %s", forbidden)
		}
	}
}
