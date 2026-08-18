package mysqlstore

import (
	"os"
	"strings"
	"testing"
)

func TestAppendBillingHourWritesUserAndTokenRowsInOneTransaction(t *testing.T) {
	b, e := os.ReadFile("billing_jobs.go")
	if e != nil {
		t.Fatal(e)
	}
	src := string(b)
	start := strings.Index(src, "func (s Store) AppendBillingHour")
	end := strings.Index(src[start:], "func (s Store) AppendBillingVerificationPage")
	if start < 0 || end < 0 {
		t.Fatal("AppendBillingHour source not found")
	}
	body := src[start : start+end]
	for _, want := range []string{"s.db.BeginTx", "INSERT INTO billing_hourly", "INSERT INTO billing_token_daily_versions", "tx.Commit()"} {
		if !strings.Contains(body, want) {
			t.Fatalf("transaction contract missing %q", want)
		}
	}
}
