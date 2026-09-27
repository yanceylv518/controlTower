package archivereader

import (
	"context"
	"errors"

	"github.com/go-sql-driver/mysql"
)

// Preserve a useful diagnosis without exposing SQL, credentials or log content.
func readFailure(err error) error {
	code := "archive_readonly_unavailable"
	var db *mysql.MySQLError
	if errors.Is(err, context.DeadlineExceeded) {
		code = "archive_read_timeout"
	} else if errors.As(err, &db) {
		switch db.Number {
		case 1054:
			code = "archive_read_schema_mismatch"
		case 1146:
			code = "archive_read_table_missing"
		case 1142, 1143:
			code = "archive_read_access_denied"
		case 3024, 1317:
			code = "archive_read_timeout"
		}
	}
	return connectionError{code: code}
}

func ReadErrorCode(err error) string {
	var e connectionError
	if errors.As(err, &e) {
		return e.code
	}
	return "archive_readonly_unavailable"
}
