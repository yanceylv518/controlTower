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

func TestWriteFailureMigrationContract(t *testing.T) {
	data, e := os.ReadFile("../../migrations/042_tuning_write_failure.sql")
	if e != nil {
		t.Fatal(e)
	}
	statements := splitSQLStatements(string(data))
	if len(statements) != 1 {
		t.Fatalf("042 must stay a single ALTER statement, got %d", len(statements))
	}
	for _, column := range []string{"write_failure_streak", "last_write_failure_at", "last_write_error"} {
		if !strings.Contains(statements[0], "ADD COLUMN "+column) {
			t.Fatalf("042 missing column %s", column)
		}
	}
}

func TestProbeRoundMemoryMigrationContract(t *testing.T) {
	data, e := os.ReadFile("../../migrations/043_tuning_probe_round_memory.sql")
	if e != nil {
		t.Fatal(e)
	}
	statements := splitSQLStatements(string(data))
	if len(statements) != 1 || !strings.Contains(statements[0], "ADD COLUMN last_probe_command_id") {
		t.Fatalf("043 must add last_probe_command_id in a single statement: %#v", statements)
	}
}

// A completed probe round must never be resurrected by a stale engine
// upsert. The guard compares against last_probe_command_id (written only by
// RecordContinuousProbeResult) because the row shape alone cannot separate
// "stale resurrect" from the engine folding results and opening a new round
// in the same persist; only the exact completed id is refused.
func TestContinuousStateProbeGuardContract(t *testing.T) {
	data, e := os.ReadFile("tuning.go")
	if e != nil {
		t.Fatal(e)
	}
	text := string(data)
	for _, required := range []string{
		"@keep_probe:=(VALUES(probe_command_id) IS NOT NULL AND VALUES(probe_command_id)=last_probe_command_id)",
		"probe_attempts=IF(@keep_probe,probe_attempts,VALUES(probe_attempts))",
		"probe_successes=IF(@keep_probe,probe_successes,VALUES(probe_successes))",
		"probe_duration_sum=IF(@keep_probe,probe_duration_sum,VALUES(probe_duration_sum))",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing probe guard contract %q", required)
		}
	}
}
