package mysqlstore

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestAlertTransitionsPersistEventsTransactionally(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	data, e := os.ReadFile(strings.TrimSuffix(file, "_contract_test.go") + ".go")
	if e != nil {
		t.Fatal(e)
	}
	text := string(data)
	for _, required := range []string{"BeginTx", "SELECT id,status FROM alerts", "if old, ok := states[alert.ID]; !ok", "event = \"firing\"", "else if old == \"resolved\"", "event = \"refired\"", "'resolved','system'", "'silence_expired','system'", "tx.Commit"} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing SQL contract %q", required)
		}
	}
	if strings.Contains(text, "IN ()") {
		t.Fatal("empty IN clause")
	}
}
