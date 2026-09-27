package archivereader

import (
	"context"
	"errors"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestReadErrorCodesDoNotExposeDriverMessages(t *testing.T) {
	for _, tc := range []struct {
		number uint16
		code   string
	}{
		{1054, "archive_read_schema_mismatch"}, {1146, "archive_read_table_missing"},
		{1142, "archive_read_access_denied"}, {3024, "archive_read_timeout"},
		{9999, "archive_readonly_unavailable"},
	} {
		err := readFailure(&mysql.MySQLError{Number: tc.number, Message: "private SQL and password"})
		if ReadErrorCode(err) != tc.code || err.Error() != tc.code || !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
	if ReadErrorCode(readFailure(context.DeadlineExceeded)) != "archive_read_timeout" {
		t.Fatal("deadline lost")
	}
}
