package preflight

import (
	"context"
	"testing"

	"controltower/agent/internal/config"
)

func TestRunSkipsMySQLWhenLogCollectionDisabled(t *testing.T) {
	result := Run(context.Background(), config.Config{
		LogCollectEnabled: false,
		DataDir:           t.TempDir(),
	})
	for _, check := range result.Checks {
		if check.Name == "mysql" && check.Status == StatusPass {
			return
		}
		if check.Name == "mysql_open" || check.Name == "mysql_ping" {
			t.Fatalf("unexpected mysql connection check: %#v", check)
		}
	}
	t.Fatal("expected skipped mysql check")
}
