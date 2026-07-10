package mysqlstore

import (
	"errors"
	"testing"

	"github.com/go-sql-driver/mysql"
)

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
