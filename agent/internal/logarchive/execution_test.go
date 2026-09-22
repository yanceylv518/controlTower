package logarchive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	af "controltower/internal/archivecontract"
	"github.com/go-sql-driver/mysql"
)

func TestArchiveDiagnosticPreservesCauseWithoutDriverText(t *testing.T) {
	for _, tc := range []struct {
		number uint16
		code   string
	}{
		{1142, "database_permission_denied"}, {1045, "database_authentication_failed"}, {1062, "database_duplicate_key"},
		{1205, "database_lock_timeout"}, {1213, "database_deadlock"}, {1146, "database_table_missing"}, {1114, "database_table_full"}, {9999, "database_error"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			cause := &mysql.MySQLError{Number: tc.number, SQLState: [5]byte{'4', '2', '0', '0', '0'}, Message: "password=synthetic-secret SQL='customer row'"}
			err := fmt.Errorf("outer: %w", &scanError{code: "source_query_failed", cause: cause, sourceID: 42, bytes: 900})
			d := Diagnose(err)
			if d.Code != tc.code || d.MySQLNumber != tc.number || d.RowID != 42 || d.RowBytes != 900 || d.SQLState != "42000" || d.Validate() != nil {
				t.Fatalf("lost cause: %+v", d)
			}
			raw, _ := json.Marshal(d)
			if strings.Contains(string(raw), "synthetic-secret") || strings.Contains(string(raw), "customer row") {
				t.Fatal("driver details leaked")
			}
			if !errors.Is(err, cause) {
				t.Fatal("wrapped cause lost")
			}
		})
	}
	if d := Diagnose(&scanError{code: "source_query_failed", cause: context.DeadlineExceeded}); d.Code != "operation_timeout" {
		t.Fatal(d)
	}
	if d := Diagnose(errors.New("password=synthetic-secret")); d.Code != "scan_failed" {
		t.Fatal("invented root cause", d)
	}
}

func TestOperationObserverReportsBeforeWorkAndDoesNotShareState(t *testing.T) {
	var seen []af.Operation
	ctx := workflowOperationContext(WithOperationObserver(context.Background(), func(o af.Operation) { seen = append(seen, o) }), "import_target", "")
	reportOperation(ctx, "read_existing_logs", "archive", "logs_202609")
	reportOperation(ctx, "rebuild_daily_statistics", "archive", "log_daily_stats")
	if len(seen) != 2 || seen[0].Table != "logs_202609" || seen[1].Table != "log_daily_stats" || seen[0].Validate() != nil {
		t.Fatal(seen)
	}
	// Unknown SQL-like identifiers cannot reach the control channel.
	reportOperation(ctx, "read_existing_logs", "archive", "logs; SELECT secret")
	if len(seen) != 2 {
		t.Fatal("unsafe identifier published")
	}
}
