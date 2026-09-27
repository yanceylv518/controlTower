package archivereader

import (
	"crypto/x509"
	"errors"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"net"
	"testing"
)

func TestConnectionFailureSanitized(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{mysql.ErrNoTLS, "archive_tls_failed"},
		{x509.UnknownAuthorityError{}, "archive_tls_failed"},
		{&mysql.MySQLError{Number: 1045, Message: "secret-password"}, "archive_auth_failed"},
		{&mysql.MySQLError{Number: 1049}, "archive_database_missing"},
		{&net.DNSError{Err: "private-host"}, "archive_network_failed"},
		{errors.New("secret-password"), "archive_connection_test_failed"},
	} {
		got := connectionFailure(fmt.Errorf("wrapped: %w", tc.err))
		if got.Error() != tc.code || ConnectionErrorCode(got) != tc.code || !errors.Is(got, ErrUnavailable) {
			t.Fatal(got)
		}
	}
}
