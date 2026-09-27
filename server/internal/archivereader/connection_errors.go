package archivereader

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"github.com/go-sql-driver/mysql"
	"net"
)

// Only stable categories cross the API boundary, never driver messages or DSNs.
type connectionError struct{ code string }

func (e connectionError) Error() string { return e.code }
func (e connectionError) Unwrap() error { return ErrUnavailable }
func ConnectionErrorCode(err error) string {
	var e connectionError
	if errors.As(err, &e) {
		return e.code
	}
	return "archive_connection_test_failed"
}
func connectionFailure(err error) error {
	code := "archive_connection_test_failed"
	var cert *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var record tls.RecordHeaderError
	var db *mysql.MySQLError
	var network net.Error
	switch {
	case errors.Is(err, mysql.ErrNoTLS), errors.As(err, &cert), errors.As(err, &unknown), errors.As(err, &hostname), errors.As(err, &invalid), errors.As(err, &record):
		code = "archive_tls_failed"
	case errors.As(err, &db):
		switch db.Number {
		case 1044, 1045, 1130:
			code = "archive_auth_failed"
		case 1049:
			code = "archive_database_missing"
		}
	case errors.As(err, &network):
		code = "archive_network_failed"
	}
	return connectionError{code: code}
}
