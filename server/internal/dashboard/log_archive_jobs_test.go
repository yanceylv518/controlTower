package dashboard

import (
	"errors"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"testing"
)

func TestArchiveJobsSchemaDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		number uint16
		want   string
	}{{1146, "archive_schema_missing"}, {1054, "archive_schema_mismatch"}, {1142, "archive_database_permission_denied"}, {1064, "archive_unavailable"}} {
		err := fmt.Errorf("query failed: %w", &mysql.MySQLError{Number: tc.number, Message: "private SQL text"})
		if got := archiveJobsErrorCode(err, "archive_unavailable"); got != tc.want {
			t.Fatalf("%d: %s", tc.number, got)
		}
	}
	if got := archiveJobsErrorCode(errors.New("secret connection detail"), "archive_days_unavailable"); got != "archive_days_unavailable" {
		t.Fatal(got)
	}
}
