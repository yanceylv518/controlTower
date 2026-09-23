package mysqlstore

import (
	"os"
	"strings"
	"testing"
)

func TestCommandSQLContract(t *testing.T) {
	data, e := os.ReadFile("command_store.go")
	if e != nil {
		t.Fatal(e)
	}
	text := string(data)
	for _, required := range []string{"BeginTx", "FOR UPDATE", "status='pending'", "status='delivered'", "status='expired'", "ON DUPLICATE KEY UPDATE", "status='submitted'", "created_at < ?", "DELETE FROM " + `"+v[0]+"`, "pruneInBatches", "LIMIT ?", "a.operation_type LIKE ? ESCAPE '!'"} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing SQL contract %q", required)
		}
	}
}
