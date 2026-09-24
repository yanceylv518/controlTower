package reporter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/go-sql-driver/mysql"
)

type HTTPStatusError struct {
	Operation  string
	StatusCode int
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("%s failed with status %d", e.Operation, e.StatusCode)
}

// SafeErrorSummary never includes raw error messages, URLs, SQL, payloads or
// filesystem paths: each may contain credentials or customer data.
func SafeErrorSummary(err error) string {
	if err == nil {
		return "none"
	}
	var status *HTTPStatusError
	if errors.As(err, &status) {
		return fmt.Sprintf("http_status=%d", status.StatusCode)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns_failure"
	}
	var network net.Error
	if errors.As(err, &network) {
		if network.Timeout() {
			return "timeout"
		}
		return "network_failure"
	}
	var database *mysql.MySQLError
	if errors.As(err, &database) {
		return fmt.Sprintf("mysql_code=%d", database.Number)
	}
	if errors.Is(err, os.ErrPermission) {
		return "permission_denied"
	}
	if errors.Is(err, os.ErrNotExist) {
		return "file_not_found"
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return "invalid_json"
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "unexpected_eof"
	}
	return fmt.Sprintf("error_type=%T", err)
}
