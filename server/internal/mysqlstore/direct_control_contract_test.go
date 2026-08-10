package mysqlstore

import (
	"os"
	"strings"
	"testing"
)

// The direct control path must leave the same paper trail as the agent
// command path while never producing rows an agent could pick up: terminal
// or delivered statuses only, never 'pending'.
func TestDirectControlSQLContract(t *testing.T) {
	data, e := os.ReadFile("tuning.go")
	if e != nil {
		t.Fatal(e)
	}
	text := string(data)
	for _, required := range []string{
		// RecordDirectWeightChange: executed write lands terminal.
		`'succeeded',?,'',?,?)`,
		// CreateContinuousProbeExecuting: in-flight direct probes are
		// 'delivered' so the expiry sweeper (pending-only) ignores them.
		`'delivered','system:auto','',?,?)`,
		"RecordDirectWeightChange",
		"CreateContinuousProbeExecuting",
		"RecordTuningPreflightResult",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing SQL contract %q", required)
		}
	}
}

func TestControlConfigSQLContract(t *testing.T) {
	data, e := os.ReadFile("instance_store.go")
	if e != nil {
		t.Fatal(e)
	}
	text := string(data)
	for _, required := range []string{
		"control_api_url<>'' AND control_api_token<>''",
		"UPDATE instances SET control_api_url=?,control_api_token=?,control_admin_user_id=?,updated_at=? WHERE COALESCE(NULLIF(site_id,''),id)=?",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing SQL contract %q", required)
		}
	}
}

func TestControlConfigMigrationContract(t *testing.T) {
	data, e := os.ReadFile("../../migrations/041_instance_control_config.sql")
	if e != nil {
		t.Fatal(e)
	}
	statements := splitSQLStatements(string(data))
	if len(statements) != 1 {
		t.Fatalf("041 must stay a single ALTER statement, got %d", len(statements))
	}
	for _, column := range []string{"control_api_url", "control_api_token", "control_admin_user_id"} {
		if !strings.Contains(statements[0], "ADD COLUMN "+column) {
			t.Fatalf("041 missing column %s", column)
		}
	}
}
