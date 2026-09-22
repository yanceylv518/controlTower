package archivecontract

import (
	"testing"
	"time"
)

func TestExecutionEvidenceValidation(t *testing.T) {
	now := time.Now().UTC()
	o := Operation{Phase: "import_target", Code: "read_existing_logs", Database: "archive", Table: "logs_202609", State: "executing", StartedAt: now}
	if o.Validate() != nil {
		t.Fatal(o)
	}
	o.FinishedAt = &now
	if o.Validate() == nil {
		t.Fatal("executing action cannot have finished")
	}
	o.State = "failed"
	d := Diagnostic{Code: "database_lock_timeout", Operation: &o, OccurredAt: now, MySQLNumber: 1205, SQLState: "HY000"}
	if d.Validate() != nil {
		t.Fatal(d)
	}
	earlier := now.Add(-time.Second)
	d.RetryAt = &earlier
	if d.Validate() == nil {
		t.Fatal("retry predates failure")
	}
	d.RetryAt = nil
	d.SQLState = "secret"
	if d.Validate() == nil {
		t.Fatal("invalid SQL state")
	}
}
