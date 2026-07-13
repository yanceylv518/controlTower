package storage

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Guards against the duplicated-column class of migration bug: a CREATE TABLE
// containing the same column twice fails with error 1060, which the migrator
// treats as ignorable for idempotent ALTER re-runs, silently leaving the
// table missing (shipped once in metric_1m; caught by the M1 stage gate).
func TestMigrationCreateTablesHaveNoDuplicateColumns(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("migrations not found: %v", err)
	}
	tableRE := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS (\w+) \((.*?)\n\)`)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range tableRE.FindAllStringSubmatch(string(data), -1) {
			table, body := match[1], match[2]
			seen := map[string]bool{}
			for _, line := range strings.Split(body, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				first := strings.ToUpper(strings.Fields(line)[0])
				if first == "PRIMARY" || first == "KEY" || first == "INDEX" || first == "UNIQUE" || first == "CONSTRAINT" {
					continue
				}
				column := strings.ToLower(strings.Fields(line)[0])
				if seen[column] {
					t.Fatalf("%s: table %s has duplicate column %q", filepath.Base(file), table, column)
				}
				seen[column] = true
			}
		}
	}
}
