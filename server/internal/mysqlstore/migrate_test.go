package mysqlstore

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestNginxRequestIDMigrationContract(t *testing.T) {
	data, err := os.ReadFile("../../migrations/009_nginx_sample_request_id.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqlText := strings.ToLower(string(data))
	for _, required := range []string{"alter table nginx_slow_samples add column request_id varchar(255) null", "create index idx_nginx_slow_samples_instance_request", "(instance_id, request_id)"} {
		if !strings.Contains(sqlText, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
	if strings.Contains(sqlText, "engine=") {
		t.Fatal("incremental migration must not rebuild the table on startup")
	}
}

func TestSplitSQLStatements(t *testing.T) {
	statements := splitSQLStatements("CREATE TABLE a (id BIGINT);\n\nCREATE INDEX idx_a_id ON a (id);\n")
	if len(statements) != 2 {
		t.Fatalf("statements len = %d, want 2: %#v", len(statements), statements)
	}
	if statements[0] != "CREATE TABLE a (id BIGINT)" {
		t.Fatalf("first statement = %q", statements[0])
	}
	if statements[1] != "CREATE INDEX idx_a_id ON a (id)" {
		t.Fatalf("second statement = %q", statements[1])
	}
}

func TestIgnorableMigrationErrorAllowsDuplicateIndexName(t *testing.T) {
	err := &mysql.MySQLError{Number: mysqlDuplicateKeyNameError, Message: "Duplicate key name 'idx_agents_instance'"}
	if !ignorableMigrationError(err) {
		t.Fatal("duplicate index error should be ignored during idempotent migration")
	}
}

func TestIgnorableMigrationErrorRejectsOtherErrors(t *testing.T) {
	if ignorableMigrationError(errors.New("boom")) {
		t.Fatal("generic error should not be ignored")
	}
	err := &mysql.MySQLError{Number: 1045, Message: "access denied"}
	if ignorableMigrationError(err) {
		t.Fatal("non-duplicate mysql error should not be ignored")
	}
}

func TestIgnorableMigrationErrorAllowsDuplicateColumnName(t *testing.T) {
	err := &mysql.MySQLError{Number: mysqlDuplicateColumnError, Message: "Duplicate column name 'next_attempt_at'"}
	if !ignorableMigrationError(err) {
		t.Fatal("duplicate column error should be ignored during idempotent migration")
	}
}

// A PK swap always "succeeds", so a bare one replays on every startup and
// rebuilds the table each time (migration 010's lesson). Riding a sentinel
// ADD COLUMN in the same ALTER makes replays fail atomically with tolerated
// error 1060 instead.
func TestAnomalyJobPKMigrationGuardsReplay(t *testing.T) {
	data, err := os.ReadFile("../../migrations/035_billing_anomaly_job_versions.sql")
	if err != nil {
		t.Fatal(err)
	}
	// No comment filtering here: ApplySQL executes every split part verbatim,
	// so a semicolon inside a comment would ship a comment-only "statement"
	// that MySQL rejects (1065) and abort startup. Exactly one statement.
	statements := splitSQLStatements(string(data))
	if len(statements) != 1 {
		t.Fatalf("PK swap must split into exactly one statement, got %d", len(statements))
	}
	s := strings.ToLower(statements[0])
	if !strings.Contains(s, "add column") || !strings.Contains(s, "drop primary key") {
		t.Fatal("PK swap must ride with a sentinel ADD COLUMN in the same ALTER; a bare DROP/ADD PRIMARY KEY rebuilds the table on every startup replay")
	}
}
