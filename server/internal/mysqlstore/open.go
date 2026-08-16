package mysqlstore

import (
	"database/sql"
	"time"

	"github.com/go-sql-driver/mysql"
)

func Open(dsn string) (*sql.DB, error) {
	normalizedDSN, err := withDatabaseTimeoutDefaults(dsn)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("mysql", normalizedDSN)
	if err != nil {
		return nil, err
	}
	return db, nil
}

// OpenForMigrations returns a connection without read/write deadlines:
// migrations legitimately run long statements (index builds, table rebuilds)
// that the 30s runtime read timeout would abort mid-flight. Only the connect
// timeout default applies; explicit DSN values always take precedence.
func OpenForMigrations(dsn string) (*sql.DB, error) {
	normalized, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	if normalized.Timeout == 0 {
		normalized.Timeout = 5 * time.Second
	}
	return sql.Open("mysql", normalized.FormatDSN())
}

func withDatabaseTimeoutDefaults(dsn string) (string, error) {
	normalized, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", err
	}
	// Background runners have no request context to cancel them. Driver-level
	// deadlines prevent one broken CT database connection from blocking the
	// single tuning loop forever. Explicit DSN values always take precedence.
	if normalized.Timeout == 0 {
		normalized.Timeout = 5 * time.Second
	}
	if normalized.ReadTimeout == 0 {
		normalized.ReadTimeout = 30 * time.Second
	}
	if normalized.WriteTimeout == 0 {
		normalized.WriteTimeout = 30 * time.Second
	}
	return normalized.FormatDSN(), nil
}
